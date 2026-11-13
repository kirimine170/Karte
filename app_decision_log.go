package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"karte/internal/canonical"
	"karte/internal/contextcore"
	"karte/internal/decisionlog"
	"karte/internal/ephyoutbox"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// ImportDecisionSummary selects a text export from the locally synced meeting room.
func (a *App) ImportDecisionSummary(sourceURL string) (*decisionlog.ImportResult, error) {
	sourceURL = strings.TrimSpace(sourceURL)
	if !decisionlog.ValidSourceURL(sourceURL) {
		return nil, fmt.Errorf("出典のHTTPS URLを入力してください")
	}
	path, err := runtime.OpenFileDialog(a.ctx, runtime.OpenDialogOptions{Title: "日次サマリを選択"})
	if err != nil || path == "" {
		return nil, err
	}
	return a.importDecisionSummary(path, sourceURL)
}

func (a *App) importDecisionSummary(path, sourceURL string) (*decisionlog.ImportResult, error) {
	data, err := decisionlog.ReadSummary(path)
	if err != nil {
		return nil, err
	}
	store, err := ephyoutbox.NewStore(a.dataDir)
	if err != nil {
		return nil, err
	}
	var result decisionlog.ImportResult
	err = canonical.WithWriter(a.dataDir, func(w *canonical.Writer) error {
		settings, err := decisionlog.LoadSettings(w)
		if err != nil {
			return err
		}
		records, err := settings.ExtractSummary(filepath.Base(path), data, sourceURL, time.Now())
		if err != nil {
			return err
		}
		policy, err := contextcore.LoadPolicy(a.dataDir)
		if err != nil {
			return err
		}
		for _, record := range records {
			if err := checkDecisionPolicy(policy, record, contextcore.CapabilityReview); err != nil {
				return err
			}
		}
		if err := decisionlog.InitializeSettings(w); err != nil {
			return err
		}
		result, err = decisionlog.Queue(w, store, records)
		return err
	})
	return &result, err
}

func (a *App) ListDecisionLog() ([]decisionlog.Record, error) {
	records := []decisionlog.Record{}
	err := canonical.WithWriter(a.dataDir, func(w *canonical.Writer) error {
		policy, err := contextcore.LoadPolicy(a.dataDir)
		if err != nil {
			return err
		}
		entries, err := decisionlog.List(w)
		if err != nil {
			return err
		}
		visible := make(map[string]bool)
		for _, entry := range entries {
			if checkDecisionPolicy(policy, entry.Record, contextcore.CapabilityRead) == nil {
				records = append(records, entry.Record)
				visible[entry.ID] = true
			}
		}
		for i := range records {
			if !visible[records[i].Supersedes] {
				records[i].Supersedes = ""
			}
			if !visible[records[i].ReplacedBy] {
				records[i].ReplacedBy = ""
			}
		}
		return nil
	})
	return records, err
}

// ExportDecisionLog writes the decision projection for the selected recipient.
func (a *App) ExportDecisionLog(recipient string) (bool, error) {
	service, err := contextcore.NewService(a.dataDir)
	if err != nil {
		return false, err
	}
	policy, err := contextcore.LoadPolicy(a.dataDir)
	if err != nil {
		return false, err
	}
	records, err := service.ExportDecisions(localHumanActor, policy, recipient)
	if err != nil {
		return false, err
	}
	data, err := json.MarshalIndent(records, "", "  ")
	if err != nil {
		return false, err
	}
	path, err := runtime.SaveFileDialog(a.ctx, runtime.SaveDialogOptions{
		Title: "決定ログを書き出す", DefaultFilename: "decisions.json",
		Filters: []runtime.FileFilter{{DisplayName: "JSON", Pattern: "*.json"}},
	})
	if err != nil || path == "" {
		return false, err
	}
	if err := os.WriteFile(path, append(data, '\n'), 0600); err != nil {
		return false, err
	}
	return true, nil
}

func (a *App) WithdrawDecision(id string) error {
	if !decisionlog.ValidID(id) {
		return fmt.Errorf("invalid decision ID")
	}
	return canonical.WithWriter(a.dataDir, func(w *canonical.Writer) error {
		entries, err := decisionlog.List(w)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if entry.ID == id {
				if err := a.authorizeDecision(entry.Record, contextcore.CapabilityReview); err != nil {
					return err
				}
				return decisionlog.Withdraw(w, id, time.Now().UTC().Format(time.RFC3339Nano))
			}
		}
		return fmt.Errorf("decision is unavailable")
	})
}

func checkDecisionPolicy(policy contextcore.Policy, r decisionlog.Record, capability contextcore.Capability) error {
	tags := []string{"decision-log", "decision:" + r.Status}
	if r.Status == decisionlog.Withdrawn || r.Status == decisionlog.Superseded {
		tags = append(tags, "decision:history")
	}
	authorization, err := policy.Authorize(localHumanActor, capability, contextcore.Resource{
		Project: r.Business, Kind: "decision", Sensitivity: r.ContextSensitivity(),
		Tags: tags, ProvenanceTypes: []string{"meeting-summary"},
	})
	if err != nil {
		return err
	}
	if !authorization.Allowed {
		return fmt.Errorf("decision is unavailable under the active Personal Context policy")
	}
	return nil
}

func (a *App) authorizeDecision(record decisionlog.Record, capability contextcore.Capability) error {
	policy, err := contextcore.LoadPolicy(a.dataDir)
	if err != nil {
		return err
	}
	return checkDecisionPolicy(policy, record, capability)
}

func (a *App) reviewDecisionProposal(proposal ephyoutbox.Proposal) (*ephyResolvedProposal, error) {
	record, err := decisionlog.DecodeProposal(proposal)
	if err != nil {
		return nil, err
	}
	if err := a.authorizeDecision(record, contextcore.CapabilityReview); err != nil {
		return nil, err
	}
	return &ephyResolvedProposal{
		Decision:        ephyoutbox.PlacementDecision{DocID: record.ID, RelativePath: decisionlog.Path(record.ID), Reason: "決定ログの候補"},
		ProposedContent: proposal.ProposedBody,
		Warnings:        []string{"候補．採用後の状態は「" + decisionlog.StatusLabel(record.Status) + "」です．"},
	}, nil
}

func (a *App) finishDecisionReview(candidateID, editedBody string, reject bool, message string) (*ephyoutbox.Receipt, error) {
	store, err := ephyoutbox.NewStore(a.dataDir)
	if err != nil {
		return nil, err
	}
	var receipt *ephyoutbox.Receipt
	err = canonical.WithWriter(a.dataDir, func(w *canonical.Writer) error {
		existing, err := store.ReadReceipt(candidateID)
		if err != nil {
			return err
		}
		if existing != nil {
			receipt = existing
			return store.MoveProposal(candidateID, receipt.Result)
		}
		proposal, err := store.ReadPending(candidateID)
		if err != nil {
			return err
		}
		original, err := decisionlog.DecodeProposal(proposal)
		if err != nil {
			return err
		}
		if err := a.authorizeDecision(original, contextcore.CapabilityReview); err != nil {
			return err
		}
		now := time.Now().UTC().Format(time.RFC3339Nano)
		receipt = &ephyoutbox.Receipt{
			SchemaVersion: ephyoutbox.SchemaVersion, CandidateID: candidateID,
			Result: "rejected", ProcessedAt: now, Message: optionalString(message),
		}

		entries, err := decisionlog.List(w)
		if err != nil {
			return err
		}
		var committed decisionlog.Entry
		for _, entry := range entries {
			if entry.ID == candidateID {
				committed = entry
				break
			}
		}
		if committed.ID == "" && !reject {
			record, err := decisionlog.Decode([]byte(editedBody))
			if err != nil {
				return err
			}
			if record.ID != original.ID || record.Business != original.Business || record.Date != original.Date ||
				record.BusinessLabel != original.BusinessLabel || record.Decider != original.Decider ||
				record.Visibility != original.Visibility || record.PublicationRestriction != original.PublicationRestriction {
				return fmt.Errorf("decision identity, date, business, reviewer, and legacy visibility are fixed for this candidate")
			}
			record.UpdatedAt, record.Sources = now, original.Sources
			if err := a.authorizeDecision(record, contextcore.CapabilityReview); err != nil {
				return err
			}
			if record.Supersedes != "" {
				for _, entry := range entries {
					if entry.ID == record.Supersedes {
						if err := a.authorizeDecision(entry.Record, contextcore.CapabilityReview); err != nil {
							return err
						}
					}
				}
			}
			committed, err = decisionlog.Accept(w, record)
			if err != nil {
				return err
			}
		}
		if committed.ID != "" {
			// A retry after the record write must finish the original acceptance,
			// even if the retry was triggered via the reject button.
			if err := a.authorizeDecision(committed.Record, contextcore.CapabilityReview); err != nil {
				return err
			}
			path := decisionlog.Path(committed.ID)
			receipt.Result, receipt.Message = "accepted", optionalString("Accepted after human review.")
			receipt.DocID, receipt.RelativePath, receipt.ResultingSHA256 = &committed.ID, &path, &committed.SHA256
		}
		if err := a.writeEphyReceipt(store, *receipt); err != nil {
			return err
		}
		return store.MoveProposal(candidateID, receipt.Result)
	})
	if err != nil {
		return nil, err
	}
	return receipt, nil
}

func (a *App) isDecisionStoragePath(path string) bool {
	return isDecisionStoragePath(a.dataDir, path)
}

func isDecisionStoragePath(root, path string) bool {
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return false
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(realRoot, resolved)
	if err != nil {
		return false
	}
	return decisionlog.IsStoragePath(rel)
}

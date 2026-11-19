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
			if err := contextcore.CheckDecisionPolicy(policy, record, contextcore.CapabilityReview); err != nil {
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
			if contextcore.CheckDecisionPolicy(policy, entry.Record, contextcore.CapabilityRead) == nil {
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

func (a *App) authorizeDecision(record decisionlog.Record, capability contextcore.Capability) error {
	policy, err := contextcore.LoadPolicy(a.dataDir)
	if err != nil {
		return err
	}
	return contextcore.CheckDecisionPolicy(policy, record, capability)
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
	options := decisionlog.ReviewOptions{
		Authorize: func(record decisionlog.Record) error {
			return a.authorizeDecision(record, contextcore.CapabilityReview)
		},
		WriteReceipt:  a.writeEphyReceipt,
		RejectMessage: optionalString(message),
		AcceptMessage: "Accepted after human review.",
	}
	if !reject {
		options.Edit = func(decisionlog.Record) (decisionlog.Record, error) {
			return decisionlog.Decode([]byte(editedBody))
		}
	}
	return decisionlog.FinishReview(a.dataDir, candidateID, options)
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

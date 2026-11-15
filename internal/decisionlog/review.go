package decisionlog

import (
	"fmt"
	"time"

	"karte/internal/canonical"
	"karte/internal/ephyoutbox"
)

type ReviewOptions struct {
	// Edit is nil when rejecting a pending decision.
	Edit          func(Record) (Record, error)
	Authorize     func(Record) error
	WriteReceipt  func(*ephyoutbox.Store, ephyoutbox.Receipt) error
	RejectMessage *string
	AcceptMessage string
}

// FinishReview commits a pending decision and finalizes its outbox receipt.
// A retry completes the original action if the decision was already written.
func FinishReview(root, candidateID string, options ReviewOptions) (*ephyoutbox.Receipt, error) {
	store, err := ephyoutbox.NewStore(root)
	if err != nil {
		return nil, err
	}
	var receipt *ephyoutbox.Receipt
	err = canonical.WithWriter(root, func(w *canonical.Writer) error {
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
		original, err := DecodeProposal(proposal)
		if err != nil {
			return err
		}
		if err := options.Authorize(original); err != nil {
			return err
		}
		now := time.Now().UTC().Format(time.RFC3339Nano)
		receipt = &ephyoutbox.Receipt{
			SchemaVersion: ephyoutbox.SchemaVersion, CandidateID: candidateID,
			Result: "rejected", ProcessedAt: now, Message: options.RejectMessage,
		}

		entries, err := List(w)
		if err != nil {
			return err
		}
		var committed Entry
		for _, entry := range entries {
			if entry.ID == candidateID {
				committed = entry
				break
			}
		}
		if committed.ID == "" && options.Edit != nil {
			record, err := options.Edit(original)
			if err != nil {
				return err
			}
			if record.ID != original.ID || record.Business != original.Business || record.Date != original.Date ||
				record.BusinessLabel != original.BusinessLabel || record.Decider != original.Decider ||
				record.Visibility != original.Visibility || record.PublicationRestriction != original.PublicationRestriction {
				return fmt.Errorf("decision identity, date, business, reviewer, and legacy visibility are fixed for this candidate")
			}
			record.UpdatedAt, record.Sources = now, original.Sources
			if err := options.Authorize(record); err != nil {
				return err
			}
			if record.Supersedes != "" {
				for _, entry := range entries {
					if entry.ID == record.Supersedes {
						if err := options.Authorize(entry.Record); err != nil {
							return err
						}
					}
				}
			}
			committed, err = Accept(w, record)
			if err != nil {
				return err
			}
		}
		if committed.ID != "" {
			if err := options.Authorize(committed.Record); err != nil {
				return err
			}
			path := Path(committed.ID)
			receipt.Result, receipt.Message = "accepted", &options.AcceptMessage
			receipt.DocID, receipt.RelativePath, receipt.ResultingSHA256 = &committed.ID, &path, &committed.SHA256
		}
		if options.WriteReceipt != nil {
			err = options.WriteReceipt(store, *receipt)
		} else {
			err = store.WriteReceipt(*receipt)
		}
		if err != nil {
			return err
		}
		return store.MoveProposal(candidateID, receipt.Result)
	})
	if err != nil {
		return nil, err
	}
	return receipt, nil
}

# Karte–Ephy reviewed outbox V1

## Review flow

Ephy atomically publishes proposal JSON to `KARTE_DATA_DIR/.mdsys/ephy/outbox/pending`．Karte's `Ephy候補` review opens validated candidates and displays operation，target document，`base_sha256`，source references，sensitivity，and either a complete create preview or an update diff．The reviewer can accept the original proposal，edit frontmatter／Markdown and accept，or reject it．No action runs automatically．

Acceptance is the only path that calls Karte's existing `SaveFile` method．Create generates a Karte-owned `doc_id` before saving．Update verifies both `target_doc_id` and the SHA-256 of current canonical file bytes before calling `SaveFile`．A mismatch produces a conflict receipt and does not write canonical content．

## Atomic processing and recovery

Proposal and receipt files are written on the same filesystem using a temporary file，flush，and rename．Karte records a short-lived transaction under `.mdsys/ephy/outbox/transactions` before saving．After `SaveFile` succeeds，the transaction records the resulting canonical SHA-256 before the receipt is attempted．

If receipt publication fails，leave the pending proposal and transaction in place，correct the storage failure，and retry the same `candidate_id` from Karte．Karte verifies the saved transaction against canonical bytes，writes the receipt，archives the proposal，and removes the transaction without calling `SaveFile` again．Do not manually copy the proposal into `content`，change the candidate ID，or delete the transaction while recovering．

Accepted proposals move to `accepted`，rejected proposals move to `rejected`，and receipts are stored in `receipts`．Conflict proposals retain their pending JSON for audit but are hidden from repeat review once the final conflict receipt exists．Ephy must submit a new candidate based on the new canonical hash．

Invalid proposal files never reach `SaveFile`．Validation errors contain filename，candidate ID when safely available，and an error code，but never proposal body text．

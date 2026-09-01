# Karte–Ephy reviewed outbox V1.1

## Review flow

Ephy atomically publishes proposal JSON to `KARTE_DATA_DIR/.mdsys/ephy/outbox/pending`．Karte's `Ephy候補` review opens validated candidates and displays operation，Karte-resolved target，placement reason and confidence，alternatives，`base_sha256`，source references，sensitivity，and either a complete create preview or an append diff．The reviewer can accept the original proposal，edit frontmatter／Markdown fragment and accept，or reject it．No action runs automatically．

Karte checks the pending outbox every five seconds while the review dialog is closed．The top-bar action displays `Ephy候補 (N)` when validated pending proposals exist．Opening the dialog refreshes immediately，and background refresh is suspended while the reviewer is editing so a poll cannot overwrite reviewed frontmatter or body text．

For a local unpackaged acceptance build，run `bash scripts/build_local_app.sh` and start `build/bin/karte` with the intended `KARTE_DATA_DIR`．A compatible Wails CLI may instead create the packaged application with `wails build`．

Acceptance is the only path that calls Karte's existing `SaveFile` method．Create derives a deterministic Karte-owned `doc_id` from the unique candidate identity，then applies `content/projects/<project>/<kind>/<YYYY-MM>/<preferred_filename>`．A path owned by another `doc_id` receives `--<doc_id先頭8文字>` before `.md`，extending the prefix only on another collision．Append verifies `target_doc_id`，project，kind，and the SHA-256 of current canonical file bytes before appending the reviewed fragment at document end．A mismatch produces a conflict receipt and does not write canonical content．

## Placement and consultation

`project`，`kind`，`year_month`，confidence，and a path-safe filename candidate are mandatory．V1.1 kinds are `note`，`meeting`，`decision`，`plan`，`task`，`research`，`reference`，`report`，`person`，`organization`，and `journal`．Tags remain independent cross-directory search metadata．Cross-project people，organizations，and journals use the `master` project when Ephy and the user classify them that way．

Ephy may retain up to three project／kind alternatives．When classification is unresolved or a similar document may be a better append target，`consultation_required` is true and Ephy must ask the user before publication．Both Ephy and Karte reject unresolved proposals from the executable review path．Sensitivity is displayed and retained but does not silently alter placement in V1.1．

Ephy recommends append only when an exact `doc_id` match also agrees on project and kind and supplies the current canonical byte hash．A similar document without exact identity，or an identity whose content classification disagrees，requires consultation．No exact or similar match produces a create recommendation．

## Atomic processing and recovery

Proposal and receipt files are written on the same filesystem using a temporary file，flush，and rename．Karte records a short-lived transaction under `.mdsys/ephy/outbox/transactions` before saving．After `SaveFile` succeeds，the transaction records the resulting canonical SHA-256 before the receipt is attempted．

If receipt publication fails，leave the pending proposal and transaction in place，correct the storage failure，and retry the same `candidate_id` from Karte．Karte verifies the saved transaction against canonical bytes，writes the receipt，archives the proposal，and removes the transaction without calling `SaveFile` again．Do not manually copy the proposal into `content`，change the candidate ID，or delete the transaction while recovering．

Accepted proposals move to `accepted`，rejected proposals move to `rejected`，and receipts are stored in `receipts`．Conflict proposals retain their pending JSON for audit but are hidden from repeat review once the final conflict receipt exists．Ephy must submit a new candidate based on the new canonical hash．Move，rename，delete，and arbitrary-position patch operations remain disabled．

Invalid proposal files never reach `SaveFile`．Validation errors contain filename，candidate ID when safely available，and an error code，but never proposal body text．

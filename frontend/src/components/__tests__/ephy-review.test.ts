import { beforeEach, describe, expect, it, vi } from 'vitest';
import { EphyReview } from '../ephy-review';

const proposalReview = {
    proposal: {
        schema_version: '1.1' as const,
        candidate_id: 'candidate-create-001',
        operation: 'create' as const,
        target_doc_id: null,
        target_relative_path: null,
        base_sha256: null,
        append_position: null,
        proposed_frontmatter: { title: 'Synthetic memory', tags: 'fixture' },
        proposed_body: '# Synthetic\n\nBody.',
        placement: {
            project: 'ephy',
            kind: 'decision' as const,
            year_month: '2026-09',
            confidence: 0.94,
            preferred_filename: 'synthetic-memory.md',
            candidates: [{ project: 'ephy', kind: 'decision', confidence: 0.94, reason: 'Durable decision.' }],
            consultation_required: false,
            consultation_question: null,
        },
        source_refs: [{ type: 'synthetic-test', reference: 'fixture://conversation/001' }],
        sensitivity: 'restricted' as const,
        created_at: '2026-08-29T00:00:00Z',
    },
    current_content: '',
    proposed_content: '---\ntitle: Synthetic memory\n---\n# Synthetic\n\nBody.',
    diff: '--- canonical\n+++ proposal',
    current_sha256: null,
    resolved_doc_id: 'doc:created',
    resolved_relative_path: 'content/projects/ephy/decision/2026-09/synthetic-memory.md',
    routing_reason: 'Project-first policy selected project=ephy.',
    placement_alternatives: [],
    content_warnings: [],
};

function renderDom(): void {
    document.body.innerHTML = `
        <button id="ephyReviewBtn">Ephy候補</button>
        <div id="ephyReviewModal" style="display:none">
          <div id="ephyProposalErrors"></div>
          <select id="ephyProposalSelect"></select>
          <pre id="ephyProposalMetadata"></pre>
          <textarea id="ephyFrontmatterEditor"></textarea>
          <textarea id="ephyBodyEditor"></textarea>
          <section><pre id="ephyProposalPreview"></pre></section>
          <section><pre id="ephyProposalDiff"></pre></section>
          <button id="ephyAcceptBtn">採用</button>
          <button id="ephyEditAcceptBtn">編集採用</button>
          <button id="ephyRejectBtn">破棄</button>
          <button id="ephyReviewCloseBtn">閉じる</button>
        </div>`;
}

describe('EphyReview', () => {
    const acceptedReceipt = {
        schema_version: '1.1',
        candidate_id: 'candidate-create-001',
        result: 'accepted',
        doc_id: 'doc:created',
        relative_path: 'content/projects/ephy/decision/2026-09/synthetic-memory.md',
        resulting_sha256: 'a'.repeat(64),
        processed_at: '2026-09-01T00:01:00Z',
        error_code: null,
        message: null,
    };
    let api: any;

    beforeEach(() => {
        renderDom();
        api = {
            ListEphyProposals: vi.fn().mockResolvedValue({ proposals: [proposalReview], errors: [] }),
            AcceptEphyProposal: vi.fn().mockResolvedValue(acceptedReceipt),
            RejectEphyProposal: vi.fn().mockResolvedValue({ ...acceptedReceipt, result: 'rejected' }),
        };
    });

    it('shows source，sensitivity，operation，target，and complete create preview', async () => {
        const component = new EphyReview(api);
        component.init();
        await component.open();

        const metadata = document.getElementById('ephyProposalMetadata')?.textContent || '';
        expect(metadata).toContain('Operation: create');
        expect(metadata).toContain('Sensitivity: restricted');
        expect(metadata).toContain('fixture://conversation/001');
        expect(metadata).toContain('content/projects/ephy/decision/2026-09/synthetic-memory.md');
        expect(metadata).toContain('Confidence: 0.94');
        expect(metadata).toContain('Project-first policy');
        expect(document.getElementById('ephyProposalPreview')?.textContent).toContain('# Synthetic');
        expect(document.getElementById('ephyReviewBtn')?.textContent).toBe('Ephy候補 (1)');
        component.destroy();
    });

    it('supports accept，edit-and-accept，and reject without automatic action', async () => {
        const component = new EphyReview(api);
        component.init();
        await component.open();
        expect(api.AcceptEphyProposal).not.toHaveBeenCalled();
        expect(api.RejectEphyProposal).not.toHaveBeenCalled();

        document.getElementById('ephyAcceptBtn')?.click();
        await vi.waitFor(() => expect(api.AcceptEphyProposal).toHaveBeenCalledWith(
            'candidate-create-001',
            proposalReview.proposal.proposed_frontmatter,
            proposalReview.proposal.proposed_body,
        ));

        (document.getElementById('ephyFrontmatterEditor') as HTMLTextAreaElement).value = '{"title":"Edited"}';
        (document.getElementById('ephyBodyEditor') as HTMLTextAreaElement).value = '# Edited';
        document.getElementById('ephyEditAcceptBtn')?.click();
        await vi.waitFor(() => expect(api.AcceptEphyProposal).toHaveBeenCalledWith(
            'candidate-create-001',
            { title: 'Edited' },
            '# Edited',
        ));

        document.getElementById('ephyRejectBtn')?.click();
        await vi.waitFor(() => expect(api.RejectEphyProposal).toHaveBeenCalledWith(
            'candidate-create-001',
            'Rejected after human review.',
        ));
        component.destroy();
    });

    it('checks for new proposals while the review dialog is closed', async () => {
        vi.useFakeTimers();
        api.ListEphyProposals
            .mockResolvedValueOnce({ proposals: [], errors: [] })
            .mockResolvedValue({ proposals: [proposalReview], errors: [] });
        const component = new EphyReview(api);
        component.init();
        await vi.waitFor(() => expect(api.ListEphyProposals).toHaveBeenCalledTimes(1));
        expect(document.getElementById('ephyReviewBtn')?.textContent).toBe('Ephy候補');

        await vi.advanceTimersByTimeAsync(5000);
        await Promise.resolve();
        expect(document.getElementById('ephyReviewBtn')?.textContent).toBe('Ephy候補 (1)');

        component.destroy();
        vi.useRealTimers();
    });
});

import { BaseComponent } from './component-base';
import type { EphyProposalReview, EphyReceipt, WailsAppAPI } from '../types/wails-api';
import { useUIStore } from '../stores/index';
import { eventLogger } from '../utils/event-logger';

export class EphyReview extends BaseComponent {
    private readonly api: WailsAppAPI;
    private readonly unsubscribe: Array<() => void> = [];
    private proposals: EphyProposalReview[] = [];
    private selected: EphyProposalReview | null = null;
    private openButton: HTMLButtonElement | null = null;
    private modal: HTMLElement | null = null;
    private proposalSelect: HTMLSelectElement | null = null;
    private metadata: HTMLElement | null = null;
    private errors: HTMLElement | null = null;
    private frontmatterEditor: HTMLTextAreaElement | null = null;
    private bodyEditor: HTMLTextAreaElement | null = null;
    private preview: HTMLElement | null = null;
    private diff: HTMLElement | null = null;
    private acceptButton: HTMLButtonElement | null = null;
    private editAcceptButton: HTMLButtonElement | null = null;
    private rejectButton: HTMLButtonElement | null = null;
    private closeButton: HTMLButtonElement | null = null;

    constructor(api: WailsAppAPI, parent?: HTMLElement) {
        super(parent);
        this.api = api;
    }

    init(): void {
        this.openButton = document.getElementById('ephyReviewBtn') as HTMLButtonElement | null;
        this.modal = document.getElementById('ephyReviewModal');
        this.proposalSelect = document.getElementById('ephyProposalSelect') as HTMLSelectElement | null;
        this.metadata = document.getElementById('ephyProposalMetadata');
        this.errors = document.getElementById('ephyProposalErrors');
        this.frontmatterEditor = document.getElementById('ephyFrontmatterEditor') as HTMLTextAreaElement | null;
        this.bodyEditor = document.getElementById('ephyBodyEditor') as HTMLTextAreaElement | null;
        this.preview = document.getElementById('ephyProposalPreview');
        this.diff = document.getElementById('ephyProposalDiff');
        this.acceptButton = document.getElementById('ephyAcceptBtn') as HTMLButtonElement | null;
        this.editAcceptButton = document.getElementById('ephyEditAcceptBtn') as HTMLButtonElement | null;
        this.rejectButton = document.getElementById('ephyRejectBtn') as HTMLButtonElement | null;
        this.closeButton = document.getElementById('ephyReviewCloseBtn') as HTMLButtonElement | null;

        if (this.openButton) {
            this.unsubscribe.push(this.addEventListener(this.openButton, 'click', () => void this.open()));
        }
        if (this.closeButton) {
            this.unsubscribe.push(this.addEventListener(this.closeButton, 'click', () => this.close()));
        }
        if (this.proposalSelect) {
            this.unsubscribe.push(this.addEventListener(this.proposalSelect, 'change', () => this.selectProposal(this.proposalSelect?.value || '')));
        }
        if (this.acceptButton) {
            this.unsubscribe.push(this.addEventListener(this.acceptButton, 'click', () => void this.accept(false)));
        }
        if (this.editAcceptButton) {
            this.unsubscribe.push(this.addEventListener(this.editAcceptButton, 'click', () => void this.accept(true)));
        }
        if (this.rejectButton) {
            this.unsubscribe.push(this.addEventListener(this.rejectButton, 'click', () => void this.reject()));
        }
        this.updateButtons();
    }

    async open(): Promise<void> {
        if (this.modal) {
            this.modal.style.display = 'flex';
            this.modal.setAttribute('aria-hidden', 'false');
        }
        await this.refresh();
    }

    close(): void {
        if (this.modal) {
            this.modal.style.display = 'none';
            this.modal.setAttribute('aria-hidden', 'true');
        }
    }

    async refresh(): Promise<void> {
        try {
            const inbox = await this.api.ListEphyProposals();
            this.proposals = inbox.proposals || [];
            if (this.errors) {
                this.errors.textContent = (inbox.errors || [])
                    .map((item) => `${item.filename}: ${item.code} — ${item.message}`)
                    .join('\n');
                this.errors.style.display = inbox.errors?.length ? 'block' : 'none';
            }
            if (this.proposalSelect) {
                this.proposalSelect.replaceChildren();
                for (const review of this.proposals) {
                    const option = document.createElement('option');
                    option.value = review.proposal.candidate_id;
                    option.textContent = `${review.proposal.operation} · ${review.proposal.target_relative_path}`;
                    this.proposalSelect.append(option);
                }
            }
            this.selectProposal(this.proposals[0]?.proposal.candidate_id || '');
        } catch (error) {
            useUIStore.getState().setStatusMessage('Ephy候補の読み込みに失敗しました', 4000);
            eventLogger.log('EphyReview', 'load-error', { error: String(error) });
        }
    }

    private selectProposal(candidateId: string): void {
        this.selected = this.proposals.find((item) => item.proposal.candidate_id === candidateId) || null;
        const review = this.selected;
        if (!review) {
            if (this.metadata) this.metadata.textContent = '保留中の候補はありません．';
            if (this.frontmatterEditor) this.frontmatterEditor.value = '';
            if (this.bodyEditor) this.bodyEditor.value = '';
            if (this.preview) this.preview.textContent = '';
            if (this.diff) this.diff.textContent = '';
            this.updateButtons();
            return;
        }
        const proposal = review.proposal;
        const baseHash = proposal.base_sha256 || '(create)';
        const targetDocId = proposal.target_doc_id || '(new document)';
        const sources = proposal.source_refs.map((ref) => `${ref.type}: ${ref.reference}`).join('\n');
        if (this.metadata) {
            this.metadata.textContent = [
                `Candidate: ${proposal.candidate_id}`,
                `Operation: ${proposal.operation}`,
                `Target: ${proposal.target_relative_path}`,
                `doc_id: ${targetDocId}`,
                `Base SHA-256: ${baseHash}`,
                `Sensitivity: ${proposal.sensitivity}`,
                `Sources:\n${sources}`,
            ].join('\n');
        }
        if (this.frontmatterEditor) {
            this.frontmatterEditor.value = JSON.stringify(proposal.proposed_frontmatter, null, 2);
        }
        if (this.bodyEditor) {
            this.bodyEditor.value = proposal.proposed_body;
        }
        if (this.preview) {
            this.preview.textContent = review.proposed_content;
            this.preview.parentElement?.classList.toggle('ephy-hidden', proposal.operation !== 'create');
        }
        if (this.diff) {
            this.diff.textContent = review.diff;
            this.diff.parentElement?.classList.toggle('ephy-hidden', proposal.operation !== 'update');
        }
        this.updateButtons();
    }

    private async accept(edited: boolean): Promise<void> {
        if (!this.selected) return;
        const proposal = this.selected.proposal;
        let frontmatter = proposal.proposed_frontmatter;
        let body = proposal.proposed_body;
        if (edited) {
            try {
                frontmatter = JSON.parse(this.frontmatterEditor?.value || '{}') as Record<string, unknown>;
            } catch {
                useUIStore.getState().setStatusMessage('frontmatter JSONを確認してください', 4000);
                return;
            }
            body = this.bodyEditor?.value ?? '';
        }
        try {
            const receipt = await this.api.AcceptEphyProposal(proposal.candidate_id, frontmatter, body);
            this.showReceipt(receipt);
            await this.refresh();
        } catch (error) {
            useUIStore.getState().setStatusMessage('Ephy候補の採用に失敗しました', 4000);
            eventLogger.log('EphyReview', 'accept-error', { candidateId: proposal.candidate_id, error: String(error) });
        }
    }

    private async reject(): Promise<void> {
        if (!this.selected) return;
        const candidateId = this.selected.proposal.candidate_id;
        try {
            const receipt = await this.api.RejectEphyProposal(candidateId, 'Rejected after human review.');
            this.showReceipt(receipt);
            await this.refresh();
        } catch (error) {
            useUIStore.getState().setStatusMessage('Ephy候補の破棄に失敗しました', 4000);
            eventLogger.log('EphyReview', 'reject-error', { candidateId, error: String(error) });
        }
    }

    private showReceipt(receipt: EphyReceipt): void {
        if (receipt.result === 'accepted') {
            useUIStore.getState().setStatusMessage('Ephy候補を採用しました', 3000);
        } else if (receipt.result === 'rejected') {
            useUIStore.getState().setStatusMessage('Ephy候補を破棄しました', 3000);
        } else if (receipt.result === 'conflict') {
            useUIStore.getState().setStatusMessage(`競合のため保存しませんでした: ${receipt.message || receipt.error_code || ''}`, 6000);
        } else {
            useUIStore.getState().setStatusMessage(`無効な候補です: ${receipt.message || receipt.error_code || ''}`, 6000);
        }
    }

    private updateButtons(): void {
        const disabled = this.selected === null;
        if (this.acceptButton) this.acceptButton.disabled = disabled;
        if (this.editAcceptButton) this.editAcceptButton.disabled = disabled;
        if (this.rejectButton) this.rejectButton.disabled = disabled;
    }

    destroy(): void {
        this.unsubscribe.forEach((unsubscribe) => unsubscribe());
        this.unsubscribe.length = 0;
    }
}

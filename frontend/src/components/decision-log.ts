import type { DecisionRecord, WailsAppAPI, WailsRuntimeAPI } from '../types/wails-api';

const statusLabels: Record<DecisionRecord['status'], string> = {
    open: '未決', decided: '決定', withdrawn: '撤回', superseded: '置換',
};

export class DecisionLog {
    private records: DecisionRecord[] = [];
    private readonly business: HTMLSelectElement;
    private readonly status: HTMLSelectElement;
    private readonly rows: HTMLElement;
    private readonly feedback: HTMLElement;

    constructor(
        private readonly root: HTMLDetailsElement,
        private readonly api: WailsAppAPI,
        private readonly runtime: WailsRuntimeAPI,
        refreshProposals: () => Promise<void>,
        signal: AbortSignal,
    ) {
        this.business = root.querySelector<HTMLSelectElement>('#decisionBusiness')!;
        this.status = root.querySelector<HTMLSelectElement>('#decisionStatus')!;
        this.rows = root.querySelector('#decisionRows')!;
        this.feedback = root.querySelector('#decisionFeedback')!;
        for (const [id, label] of Object.entries(statusLabels)) {
            this.status.add(new Option(label, id));
        }
        this.status.value = 'open';
        this.business.addEventListener('change', () => this.render(), { signal });
        this.status.addEventListener('change', () => this.render(), { signal });
        root.addEventListener('toggle', () => void this.refresh(), { signal });
        const form = root.querySelector<HTMLFormElement>('#decisionImportForm')!;
        form.addEventListener('submit', async (event) => {
            event.preventDefault();
            const button = form.querySelector<HTMLButtonElement>('button')!;
            const source = form.querySelector<HTMLInputElement>('input')!;
            button.disabled = true;
            this.feedback.textContent = '';
            try {
                const result = await this.api.ImportDecisionSummary(source.value);
                if (result) {
                    this.feedback.textContent = `候補 ${result.added}件を追加，出典 ${result.merged}件を統合，変更なし ${result.unchanged}件．`;
                    await refreshProposals();
                }
            } catch (error) {
                this.feedback.textContent = `取り込みに失敗しました．${String(error)}`;
            } finally {
                button.disabled = false;
            }
        }, { signal });
    }

    async refresh(): Promise<void> {
        if (!this.root.open) return;
        try {
            this.records = await this.api.ListDecisionLog();
            const previous = this.business.value;
            const labels = new Map(this.records.map((record) => [record.business, record.business_label || record.business]));
            this.business.replaceChildren(new Option('すべて', ''));
            for (const [id, label] of Array.from(labels).sort((a, b) => a[1].localeCompare(b[1], 'ja'))) {
                this.business.add(new Option(label, id));
            }
            this.business.value = labels.has(previous) ? previous : '';
            this.render();
        } catch {
            this.records = [];
            this.rows.textContent = '決定ログを読み込めませんでした．';
        }
    }

    private render(): void {
        const rows = this.records.filter((record) =>
            (!this.business.value || record.business === this.business.value) &&
            (!this.status.value || record.status === this.status.value)
        );
        this.rows.replaceChildren();
        if (rows.length === 0) {
            this.rows.textContent = '該当する記録はありません．';
            return;
        }
        const list = document.createElement('ul');
        for (const record of rows) {
            const row = document.createElement('li');
            const text = document.createElement('p');
            text.textContent = record.content;
            const metadata = document.createElement('small');
            metadata.textContent = `${record.business_label || record.business} · ${statusLabels[record.status]} · ${record.date} · 更新 ${new Date(record.updated_at).toLocaleDateString('ja-JP')}`;
            row.append(text, metadata);
            const actions = document.createElement('div');
            record.sources.forEach(({ url }, index) => {
                const link = document.createElement('a');
                link.href = url;
                link.textContent = record.sources.length === 1 ? '出典' : `出典 ${index + 1}`;
                link.addEventListener('click', (event) => {
                    event.preventDefault();
                    this.runtime.BrowserOpenURL(url);
                });
                actions.append(link, ' ');
            });
            if (record.status === 'open' || record.status === 'decided') {
                const button = document.createElement('button');
                button.type = 'button';
                button.className = 'btn btn-secondary';
                button.textContent = '撤回';
                button.addEventListener('click', async () => {
                    if (!window.confirm('この記録を撤回しますか？')) return;
                    button.disabled = true;
                    try {
                        await this.api.WithdrawDecision(record.decision_id);
                        this.feedback.textContent = '記録を撤回しました．';
                        await this.refresh();
                    } catch {
                        this.feedback.textContent = '記録を撤回できませんでした．';
                        button.disabled = false;
                    }
                });
                actions.append(button);
            }
            const identity = document.createElement('details');
            const summary = document.createElement('summary');
            summary.textContent = '記録ID';
            const ids = document.createElement('code');
            ids.textContent = [record.decision_id, record.supersedes && `置換元 ${record.supersedes}`, record.replaced_by && `置換先 ${record.replaced_by}`].filter(Boolean).join(' · ');
            identity.append(summary, ids);
            row.append(actions, identity);
            list.append(row);
        }
        this.rows.append(list);
    }
}

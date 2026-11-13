import type { DecisionRecord, GoogleAccountStatus, GoogleSyncStatus, WailsAppAPI, WailsRuntimeAPI } from '../types/wails-api';

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
        const exportForm = root.querySelector<HTMLFormElement>('#decisionExportForm')!;
        exportForm.addEventListener('submit', async (event) => {
            event.preventDefault();
            const button = exportForm.querySelector<HTMLButtonElement>('button')!;
            const recipient = exportForm.querySelector<HTMLSelectElement>('select')!;
            button.disabled = true;
            this.feedback.textContent = '';
            try {
                if (await this.api.ExportDecisionLog(recipient.value)) {
                    this.feedback.textContent = '決定ログを書き出しました．';
                }
            } catch {
                this.feedback.textContent = '決定ログを書き出せませんでした．';
            } finally {
                button.disabled = false;
            }
        }, { signal });
        this.bindGoogleAccount(signal);
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

    private bindGoogleAccount(signal: AbortSignal): void {
        const panel = this.root.querySelector<HTMLDetailsElement>('#decisionGoogleAccount');
        if (!panel) return;
        const status = panel.querySelector<HTMLElement>('[data-google-status]')!;
        const signIn = panel.querySelector<HTMLButtonElement>('[data-google-sign-in]')!;
        const signOut = panel.querySelector<HTMLButtonElement>('[data-google-sign-out]')!;
        const syncPanel = panel.querySelector<HTMLFormElement>('#decisionGoogleSync')!;
        const refreshSync = this.bindGoogleSync(syncPanel, signal);
        const render = (value: GoogleAccountStatus) => {
            status.textContent = value.connected ? value.email || 'ログイン済み' : '未ログイン';
            signIn.hidden = value.connected;
            signOut.hidden = !value.connected;
            syncPanel.hidden = !value.connected;
            if (value.connected) void refreshSync();
        };
        const run = async (operation: () => Promise<GoogleAccountStatus>) => {
            signIn.disabled = signOut.disabled = true;
            try {
                render(await operation());
            } catch (error) {
                status.textContent = String(error);
            } finally {
                signIn.disabled = signOut.disabled = false;
            }
        };
        panel.addEventListener('toggle', () => {
            if (panel.open && !signIn.disabled) void run(() => this.api.GoogleAccountStatus());
        }, { signal });
        signIn.addEventListener('click', () => void run(() => this.api.SignInGoogle()), { signal });
        signOut.addEventListener('click', () => void run(async () => {
            await this.api.SignOutGoogle();
            return { connected: false };
        }), { signal });
    }

    private bindGoogleSync(form: HTMLFormElement, signal: AbortSignal): () => Promise<void> {
        const name = form.querySelector<HTMLInputElement>('input')!;
        const enable = form.querySelector<HTMLButtonElement>('[data-sync-enable]')!;
        const disable = form.querySelector<HTMLButtonElement>('[data-sync-disable]')!;
        const now = form.querySelector<HTMLButtonElement>('[data-sync-now]')!;
        const status = form.querySelector<HTMLElement>('[data-sync-status]')!;
        const render = (value: GoogleSyncStatus) => {
            if (value.name) name.value = value.name;
            name.readOnly = Boolean(value.name);
            enable.hidden = value.enabled;
            disable.hidden = now.hidden = !value.enabled;
            status.textContent = value.conflicts > 0
                ? `競合した版を ${value.conflicts} 件保存しました．`
                : value.last_sync ? `最終同期 ${new Date(value.last_sync).toLocaleString('ja-JP')}` : '';
        };
        const run = async (operation: () => Promise<GoogleSyncStatus>) => {
            enable.disabled = disable.disabled = now.disabled = true;
            try {
                render(await operation());
            } catch (error) {
                status.textContent = String(error);
            } finally {
                enable.disabled = disable.disabled = now.disabled = false;
            }
        };
        const sync = () => run(async () => {
            const value = await this.api.SyncGoogleDecisions();
            await this.refresh();
            return value;
        });
        form.addEventListener('submit', (event) => {
            event.preventDefault();
            void run(() => this.api.SetGoogleDecisionSync(name.value, true)).then(() => {
                if (!now.hidden) void sync();
            });
        }, { signal });
        now.addEventListener('click', () => void sync(), { signal });
        disable.addEventListener('click', () => void run(() => this.api.SetGoogleDecisionSync(name.value, false)), { signal });
        const stop = this.runtime.EventsOn('google-decision-sync', (value) => {
            if (!form.hidden && !now.disabled && value && typeof value === 'object') {
                render(value as GoogleSyncStatus);
                void this.refresh();
            }
        });
        signal.addEventListener('abort', stop, { once: true });
        return () => run(() => this.api.GoogleDecisionSyncStatus());
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

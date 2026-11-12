import { describe, expect, it, vi } from 'vitest';
import { DecisionLog } from '../decision-log';
import type { DecisionRecord, WailsAppAPI, WailsRuntimeAPI } from '../../types/wails-api';

describe('decision log in candidate review', () => {
    it('shows open decisions in date order and filters by business and state', async () => {
        document.body.innerHTML = `
            <details open>
                <form id="decisionImportForm"><input type="url"><button>取込</button></form>
                <select id="decisionBusiness"><option value="">すべて</option></select>
                <select id="decisionStatus"><option value="">すべて</option></select>
                <div id="decisionFeedback"></div><div id="decisionRows"></div>
            </details>`;
        const makeRecord = (overrides: Partial<DecisionRecord>): DecisionRecord => ({
            decision_id: 'fixture',
            business: 'workshop',
            business_label: '工作室',
            content: '工作室の利用時間を検討',
            status: 'open',
            date: '2026-11-12',
            updated_at: '2026-11-12T04:00:00Z',
            supersedes: '',
            replaced_by: '',
            sources: [{ url: 'https://example.invalid/meeting', sha256: 'fixture' }],
            ...overrides,
        });
        const api = {
            ListDecisionLog: vi.fn().mockResolvedValue([
                makeRecord({ date: '2026-11-11', business: 'karte', business_label: 'Karte', content: '古い未決' }),
                makeRecord({}),
                makeRecord({ status: 'decided', content: '採用済み' }),
            ]),
        } as unknown as WailsAppAPI;
        const runtime = { BrowserOpenURL: vi.fn() } as unknown as WailsRuntimeAPI;
        const events = new AbortController();
        const log = new DecisionLog(document.querySelector('details')!, api, runtime, vi.fn(), events.signal);

        try {
            await log.refresh();
            const rows = document.getElementById('decisionRows')!;
            expect(Array.from(rows.querySelectorAll('li p'), (row) => row.textContent)).toEqual(['古い未決', '工作室の利用時間を検討']);
            expect(rows.textContent).toContain('更新');
            const business = document.querySelector<HTMLSelectElement>('#decisionBusiness')!;
            business.value = 'workshop';
            business.dispatchEvent(new Event('change'));
            expect(rows.textContent).not.toContain('古い未決');
            rows.querySelector('a')!.click();
            expect(runtime.BrowserOpenURL).toHaveBeenCalledWith('https://example.invalid/meeting');

            const status = document.querySelector<HTMLSelectElement>('#decisionStatus')!;
            status.value = 'decided';
            status.dispatchEvent(new Event('change'));
            expect(rows.textContent).toContain('採用済み');
            expect(rows.textContent).not.toContain('工作室の利用時間を検討');
        } finally {
            events.abort();
            document.body.replaceChildren();
        }
    });
});

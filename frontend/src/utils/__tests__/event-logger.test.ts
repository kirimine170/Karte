import { beforeEach, describe, expect, it } from 'vitest';
import { eventLogger } from '../event-logger';

describe('EventLogger privacy', () => {
	beforeEach(() => eventLogger.clearLogs());

	it('keeps aggregate metadata and removes content-bearing state', () => {
		eventLogger.log('Sidebar', 'search-input', {
			query: 'private search query',
			path: 'content/projects/private/secret.md',
			candidateId: 'candidate-private-id',
			error: 'private error text',
			count: 2,
			reason: 'board-autosave',
		});
		const [entry] = eventLogger.getLogs();
		expect(entry.state).toEqual({ error: 'redacted', count: 2, reason: 'board-autosave' });
		expect(JSON.stringify(entry)).not.toContain('private search query');
		expect(JSON.stringify(entry)).not.toContain('content/projects/private');
		expect(JSON.stringify(entry)).not.toContain('candidate-private-id');
		expect(JSON.stringify(entry)).not.toContain('private error text');
	});

	it('redacts content-bearing component and action labels', () => {
		eventLogger.log('Private person name', 'content/projects/private/secret.md');
		const [entry] = eventLogger.getLogs();
		expect(entry.component).toBe('unknown');
		expect(entry.action).toBe('unknown');
	});
});

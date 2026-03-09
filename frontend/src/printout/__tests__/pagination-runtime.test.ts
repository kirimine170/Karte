import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import '../pagination-runtime';

interface TestWindow extends Window {
  __karteCreatePrintoutPagination?: (doc?: Document) => {
    buildPages(): void;
  };
  __karteRunPrintoutPagination?: unknown;
  __kartePrintoutReady?: unknown;
  __kartePrintoutError?: unknown;
}

describe('printout pagination runtime', () => {
  let clientHeightDescriptor: PropertyDescriptor | undefined;
  let scrollHeightDescriptor: PropertyDescriptor | undefined;
  let rectDescriptor: PropertyDescriptor | undefined;

  beforeEach(() => {
    clientHeightDescriptor = Object.getOwnPropertyDescriptor(HTMLElement.prototype, 'clientHeight');
    scrollHeightDescriptor = Object.getOwnPropertyDescriptor(HTMLElement.prototype, 'scrollHeight');
    rectDescriptor = Object.getOwnPropertyDescriptor(HTMLElement.prototype, 'getBoundingClientRect');

    Object.defineProperty(HTMLElement.prototype, 'clientHeight', {
      configurable: true,
      get() {
        if (this.classList && this.classList.contains('karte-print-page-content')) {
          return 100;
        }
        return Number(this.dataset.height || 0);
      }
    });

    Object.defineProperty(HTMLElement.prototype, 'scrollHeight', {
      configurable: true,
      get() {
        if (this.classList && this.classList.contains('karte-print-page-content')) {
          return Array.from(this.children).reduce((total, child) => total + Number((child as HTMLElement).dataset.height || 0), 0);
        }
        return Number(this.dataset.height || 0);
      }
    });

    Object.defineProperty(HTMLElement.prototype, 'getBoundingClientRect', {
      configurable: true,
      value() {
        const height = Number((this as HTMLElement).dataset.height || (this as HTMLElement).scrollHeight || 0);
        return { x: 0, y: 0, width: 0, height, top: 0, right: 0, bottom: height, left: 0, toJSON() { return {}; } };
      }
    });
  });

  afterEach(() => {
    if (clientHeightDescriptor) {
      Object.defineProperty(HTMLElement.prototype, 'clientHeight', clientHeightDescriptor);
    } else {
      delete (HTMLElement.prototype as Partial<typeof HTMLElement.prototype>).clientHeight;
    }
    if (scrollHeightDescriptor) {
      Object.defineProperty(HTMLElement.prototype, 'scrollHeight', scrollHeightDescriptor);
    } else {
      delete (HTMLElement.prototype as Partial<typeof HTMLElement.prototype>).scrollHeight;
    }
    if (rectDescriptor) {
      Object.defineProperty(HTMLElement.prototype, 'getBoundingClientRect', rectDescriptor);
    } else {
      delete (HTMLElement.prototype as Partial<typeof HTMLElement.prototype>).getBoundingClientRect;
    }

    const runtimeWindow = window as TestWindow;
    delete runtimeWindow.__karteCreatePrintoutPagination;
    delete runtimeWindow.__karteRunPrintoutPagination;
    delete runtimeWindow.__kartePrintoutReady;
    delete runtimeWindow.__kartePrintoutError;
  });

  it('preserves list wrapper and direct text when splitting an oversized first block', () => {
    document.documentElement.setAttribute('data-printout', 'A4');
    document.body.innerHTML = `
      <article>
        <ul>
          <li data-height="150">Lead text<span data-height="60">Part A</span><span data-height="60">Part B</span></li>
        </ul>
      </article>
    `;

    const runtimeWindow = window as TestWindow;
    const pagination = runtimeWindow.__karteCreatePrintoutPagination?.(document);
    pagination?.buildPages();

    expect(document.querySelectorAll('section.karte-print-page').length).toBeGreaterThan(1);
    expect(document.querySelector('article ul > li')).toBeTruthy();
    expect(document.querySelector('article')?.textContent).toContain('Lead text');
  });
});

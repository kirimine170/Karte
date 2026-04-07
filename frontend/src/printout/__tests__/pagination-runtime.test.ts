import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import '../pagination-runtime';

interface TestWindow extends Window {
  __karteCreatePrintoutPagination?: (doc?: Document) => {
    buildPages(): void;
  };
  __kartePrintoutReady?: unknown;
  __kartePrintoutError?: unknown;
}

describe('printout pagination runtime', () => {
  let clientHeightDescriptor: PropertyDescriptor | undefined;
  let scrollHeightDescriptor: PropertyDescriptor | undefined;
  let rectDescriptor: PropertyDescriptor | undefined;

  function measureNode(node: Node): number {
    if (!(node instanceof HTMLElement)) {
      return 0;
    }
    const ownHeight = Number(node.dataset.height || 0);
    if (ownHeight > 0) {
      return ownHeight;
    }
    return Array.from(node.childNodes).reduce((total, child) => total + measureNode(child), 0);
  }

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
          return Array.from(this.childNodes).reduce((total, child) => total + measureNode(child), 0);
        }
        return measureNode(this);
      }
    });

    Object.defineProperty(HTMLElement.prototype, 'getBoundingClientRect', {
      configurable: true,
      value() {
        const height = measureNode(this as HTMLElement);
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

  it('preserves direct text nodes on a split wrapper block', () => {
    document.documentElement.setAttribute('data-printout', 'A4');
    document.body.innerHTML = `
      <article>
        <div>Lead text<span data-height="60">Part A</span><span data-height="60">Part B</span></div>
      </article>
    `;

    const runtimeWindow = window as TestWindow;
    const pagination = runtimeWindow.__karteCreatePrintoutPagination?.(document);
    pagination?.buildPages();

    expect(document.querySelectorAll('section.karte-print-page').length).toBeGreaterThan(1);
    expect(document.querySelector('article .karte-print-page-content > div')).toBeTruthy();
    expect(document.querySelector('article .karte-print-page-content > div')?.textContent).toContain('Lead text');
  });

  it('falls back to karte-printout meta when data-printout is missing', () => {
    document.documentElement.removeAttribute('data-printout');
    document.head.innerHTML = '<meta name="karte-printout" content="B5">';
    document.body.innerHTML = `
      <article>
        <p data-height="80">A</p>
        <p data-height="80">B</p>
      </article>
    `;

    const runtimeWindow = window as TestWindow;
    const pagination = runtimeWindow.__karteCreatePrintoutPagination?.(document);
    pagination?.buildPages();

    expect(document.documentElement.getAttribute('data-printout')).toBe('B5');
    expect(document.querySelectorAll('section.karte-print-page').length).toBeGreaterThan(1);
  });

  it('does not paginate in infinite mode', () => {
    document.documentElement.setAttribute('data-printout', 'infinite');
    document.body.innerHTML = `
      <article>
        <p data-height="80">A</p>
        <p data-height="80">B</p>
      </article>
    `;

    const runtimeWindow = window as TestWindow;
    const pagination = runtimeWindow.__karteCreatePrintoutPagination?.(document);
    pagination?.buildPages();

    expect(document.querySelectorAll('section.karte-print-page').length).toBe(0);
  });

  it('rebuilds snapshot when stored original html is empty', () => {
    document.documentElement.setAttribute('data-printout', 'B5');
    document.body.innerHTML = `
      <article data-karte-print-original-html="">
        <p data-height="80">A</p>
        <p data-height="80">B</p>
      </article>
    `;

    const runtimeWindow = window as TestWindow;
    const pagination = runtimeWindow.__karteCreatePrintoutPagination?.(document);
    pagination?.buildPages();

    expect(document.querySelectorAll('section.karte-print-page').length).toBeGreaterThan(1);
  });

  it('paginates using main container when article is absent', () => {
    document.documentElement.setAttribute('data-printout', 'B5');
    document.body.innerHTML = `
      <main class="container">
        <h1 data-height="90">Title</h1>
        <p data-height="90">A</p>
        <p data-height="90">B</p>
      </main>
    `;

    const runtimeWindow = window as TestWindow;
    const pagination = runtimeWindow.__karteCreatePrintoutPagination?.(document);
    pagination?.buildPages();

    expect(document.querySelectorAll('section.karte-print-page').length).toBeGreaterThan(1);
    expect(document.querySelector('main.container.karte-print-flow-root')).toBeTruthy();
  });

  it('forces page break when marker appears between blocks', () => {
    document.documentElement.setAttribute('data-printout', 'B5');
    document.body.innerHTML = `
      <article>
        <p data-height="60">Before</p>
        <div class="karte-force-page-break" aria-hidden="true"></div>
        <p data-height="60">After</p>
      </article>
    `;

    const runtimeWindow = window as TestWindow;
    const pagination = runtimeWindow.__karteCreatePrintoutPagination?.(document);
    pagination?.buildPages();

    const pages = document.querySelectorAll('section.karte-print-page');
    expect(pages.length).toBe(2);
    expect(pages[0]?.textContent).toContain('Before');
    expect(pages[1]?.textContent).toContain('After');
  });

  it('does not create empty trailing pages for consecutive or tail markers', () => {
    document.documentElement.setAttribute('data-printout', 'B5');
    document.body.innerHTML = `
      <article>
        <p data-height="60">A</p>
        <div class="karte-force-page-break" aria-hidden="true"></div>
        <div class="karte-force-page-break" aria-hidden="true"></div>
        <p data-height="60">B</p>
        <div class="karte-force-page-break" aria-hidden="true"></div>
      </article>
    `;

    const runtimeWindow = window as TestWindow;
    const pagination = runtimeWindow.__karteCreatePrintoutPagination?.(document);
    pagination?.buildPages();

    const pages = document.querySelectorAll('section.karte-print-page');
    expect(pages.length).toBe(2);
    expect(pages[0]?.textContent).toContain('A');
    expect(pages[1]?.textContent).toContain('B');
  });
});

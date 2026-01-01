// コンポーネント基底クラス（型安全なDOM操作のためのユーティリティ）

export interface ComponentLifecycle {
    init(): void;
    destroy(): void;
}

export abstract class BaseComponent implements ComponentLifecycle {
    protected element: HTMLElement | null = null;
    protected parent: HTMLElement | null = null;

    constructor(parent?: HTMLElement) {
        this.parent = parent || null;
    }

    abstract init(): void;
    abstract destroy(): void;

    protected querySelector<T extends HTMLElement = HTMLElement>(selector: string): T | null {
        if (!this.element) return null;
        return this.element.querySelector<T>(selector);
    }

    protected querySelectorAll<T extends HTMLElement = HTMLElement>(selector: string): T[] {
        if (!this.element) return [];
        return Array.from(this.element.querySelectorAll<T>(selector));
    }

    protected createElement<T extends HTMLElement = HTMLElement>(
        tag: string,
        className?: string,
        textContent?: string
    ): T {
        const el = document.createElement(tag) as T;
        if (className) {
            el.className = className;
        }
        if (textContent) {
            el.textContent = textContent;
        }
        return el;
    }

    protected addEventListener<K extends keyof HTMLElementEventMap>(
        element: HTMLElement,
        type: K,
        listener: (this: HTMLElement, ev: HTMLElementEventMap[K]) => void,
        options?: boolean | AddEventListenerOptions
    ): () => void {
        element.addEventListener(type, listener, options);
        return () => {
            element.removeEventListener(type, listener, options);
        };
    }

    protected setAttribute(element: HTMLElement, name: string, value: string): void {
        element.setAttribute(name, value);
    }

    protected removeAttribute(element: HTMLElement, name: string): void {
        element.removeAttribute(name);
    }

    protected toggleClass(element: HTMLElement, className: string, condition: boolean): void {
        if (condition) {
            element.classList.add(className);
        } else {
            element.classList.remove(className);
        }
    }
}


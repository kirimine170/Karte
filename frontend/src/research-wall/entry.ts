/// <reference types="vite/client" />
// The independently runnable renderer shares its UI with the native workspace．
// @ts-ignore Static renderer template is loaded by Vite．
import template from '../../../examples/crazy-wall/index.html?raw';
import '../../../examples/crazy-wall/style.css';
import * as pdfjs from 'pdfjs-dist';
import workerURL from 'pdfjs-dist/build/pdf.worker.min.mjs?url';

const body = new DOMParser().parseFromString(template, 'text/html').body;
body.querySelectorAll('script').forEach(script => script.remove());
document.body.replaceChildren(...body.childNodes);
pdfjs.GlobalWorkerOptions.workerSrc = workerURL;
const channel = 'karte.research-wall.v1';
let sequence = 0;
const pending = new Map<string, {resolve: (value: any) => void; reject: (error: Error) => void; timer: ReturnType<typeof setTimeout>}>();
const listeners = new Set<(operation: string, value: any) => void>();
window.addEventListener('message', event => {
    if (event.source !== window.parent || event.origin !== location.origin || event.data?.channel !== channel) return;
    const {id, error, value, operation} = event.data;
    if (id && pending.has(id)) {
        const item = pending.get(id)!; pending.delete(id); clearTimeout(item.timer);
        if (error) item.reject(new Error(error)); else item.resolve(value);
    } else if (operation) listeners.forEach(listener => listener(operation, value));
});
(window as any).karteResearchHost = {
    pdf: pdfjs,
    pdfOptions: {wasmUrl: '/pdfjs/wasm/', standardFontDataUrl: '/pdfjs/standard_fonts/', cMapUrl: '/pdfjs/cmaps/', cMapPacked: true},
    call(operation: string, value?: any): Promise<any> {
        const id = `wall-${++sequence}`;
        return new Promise((resolve, reject) => {
            const timer = setTimeout(() => { pending.delete(id); reject(new Error('Karteとの通信がタイムアウトしました．再読込して確認してください．')); }, 120000);
            pending.set(id, {resolve, reject, timer});
            window.parent.postMessage({channel, id, operation, value}, location.origin);
        });
    },
    notify(operation: string, value?: any) { window.parent.postMessage({channel, operation, value}, location.origin); },
    on(listener: (operation: string, value: any) => void) { listeners.add(listener); },
};
// @ts-ignore The pure JavaScript renderer is also served by the standalone PoC．
await import('../../../examples/crazy-wall/app.mjs');

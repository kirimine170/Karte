import { beforeEach, afterEach, vi } from 'vitest';

if (typeof globalThis.DOMMatrix === 'undefined') {
    class TestDOMMatrix {
        a = 1;
        b = 0;
        c = 0;
        d = 1;
        e = 0;
        f = 0;

        multiplySelf() {
            return this;
        }

        preMultiplySelf() {
            return this;
        }

        translateSelf() {
            return this;
        }

        scaleSelf() {
            return this;
        }

        rotateSelf() {
            return this;
        }

        invertSelf() {
            return this;
        }
    }

    globalThis.DOMMatrix = TestDOMMatrix as unknown as typeof DOMMatrix;
}

if (typeof globalThis.ImageData === 'undefined') {
    class TestImageData {
        data: Uint8ClampedArray;
        width: number;
        height: number;
        colorSpace: PredefinedColorSpace = 'srgb';

        constructor(dataOrWidth: Uint8ClampedArray | number, width: number, height?: number) {
            if (typeof dataOrWidth === 'number') {
                this.width = dataOrWidth;
                this.height = width;
                this.data = new Uint8ClampedArray(this.width * this.height * 4);
            } else {
                this.data = dataOrWidth;
                this.width = width;
                this.height = height ?? 0;
            }
        }
    }

    globalThis.ImageData = TestImageData as unknown as typeof ImageData;
}

if (typeof globalThis.Path2D === 'undefined') {
    class TestPath2D {
        addPath() {}
        closePath() {}
        moveTo() {}
        lineTo() {}
        bezierCurveTo() {}
        quadraticCurveTo() {}
        arc() {}
        arcTo() {}
        ellipse() {}
        rect() {}
    }

    globalThis.Path2D = TestPath2D as unknown as typeof Path2D;
}

// DOM環境のセットアップ
beforeEach(() => {
    // 各テスト前にDOMをクリーンアップ
    document.body.innerHTML = '';
    document.head.innerHTML = '';
    
    // グローバルオブジェクトのリセット
    if (typeof window.localStorage !== 'undefined' && window.localStorage.clear) {
        window.localStorage.clear();
    }
    if (typeof window.sessionStorage !== 'undefined' && window.sessionStorage.clear) {
        window.sessionStorage.clear();
    }
});

// タイマーのモック（必要に応じて使用）
// vi.useFakeTimers();

afterEach(() => {
    // vi.clearAllTimers();
    // vi.useRealTimers();
    // vi.useFakeTimers();
});


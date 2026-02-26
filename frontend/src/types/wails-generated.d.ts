declare module '../../wailsjs/wailsjs/go/main/App' {
  const AppModule: Record<string, unknown>;
  export = AppModule;
}

declare module '../../wailsjs/wailsjs/runtime/runtime' {
  export function EventsOn(eventName: string, callback: (...args: unknown[]) => void): () => void;
  export function BrowserOpenURL(url: string): void;
}

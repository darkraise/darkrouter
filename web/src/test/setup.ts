import "@testing-library/jest-dom/vitest"

// jsdom implements neither of these, and darkraise-ui's ThemeProvider calls
// matchMedia on mount to resolve "system" mode. Without them any test that
// renders the real app shell fails inside the theme layer before it reaches
// whatever it meant to assert.
if (!window.matchMedia) {
  window.matchMedia = ((query: string) => ({
    matches: false,
    media: query,
    onchange: null,
    addListener: () => {},
    removeListener: () => {},
    addEventListener: () => {},
    removeEventListener: () => {},
    dispatchEvent: () => false,
  })) as unknown as typeof window.matchMedia
}

if (!globalThis.ResizeObserver) {
  globalThis.ResizeObserver = class {
    observe() {}
    unobserve() {}
    disconnect() {}
  } as unknown as typeof ResizeObserver
}

// jsdom defines scrollTo but throws "Not implemented" from it, so this has to
// overwrite rather than fill a gap. Left alone it prints on every render that
// scrolls and buries real warnings in the suite's output.
window.scrollTo = (() => {}) as typeof window.scrollTo

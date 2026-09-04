import { StrictMode } from "react"
import { createRoot } from "react-dom/client"
import { App } from "./app"
import { clearHiddenAxisStorage } from "./theme-storage"
import { themeConfig } from "./theme.config"
import "./styles/globals.css"

// Before the provider mounts: it reads storage in its state initialisers, so
// a value stored while an axis was still offered would otherwise outlive the
// control that set it.
clearHiddenAxisStorage(themeConfig.switcher?.axes ?? {})

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <App />
  </StrictMode>,
)

import { StrictMode } from "react"
import { createRoot } from "react-dom/client"

import App from "./App"
import { loadMessages } from "@/i18n/load-messages"
import { getLocale } from "@/i18n/locale-store"
import "./styles/globals.css"

function renderApp() {
  createRoot(document.getElementById("root")!).render(
    <StrictMode>
      <App />
    </StrictMode>,
  )
}

// Load only the active locale's catalog before the first paint. A failed
// load still renders; react-intl then falls back to message IDs.
loadMessages(getLocale())
  .catch((error: unknown) => console.error("load locale messages", error))
  .finally(renderApp)

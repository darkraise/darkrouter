import { useEffect } from "react"
import { useRouterState } from "@tanstack/react-router"
import { pageIdentity } from "./page-identity"

const APP = "Darkrouter"

/** "Providers · Darkrouter": the section first, because a row of tabs is
 *  read by its leading word and every tab of this app ends the same way. */
export function pageTitle(pathname: string): string {
  return `${pageIdentity(pathname).title} · ${APP}`
}

export function usePageTitle() {
  const pathname = useRouterState({ select: (s) => s.location.pathname })
  useEffect(() => {
    document.title = pageTitle(pathname)
  }, [pathname])
}

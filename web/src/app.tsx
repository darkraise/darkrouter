import { useEffect, useState } from "react"
import { QueryClient, QueryClientProvider, useQuery } from "@tanstack/react-query"
import { ThemeProvider } from "darkraise-ui/theme"
import { SidebarLayout } from "darkraise-ui/layout"
import { themeConfig } from "./theme.config"
import { api, onUnauthorized, setCsrfToken } from "./lib/api"
import { ApiError } from "./lib/api"
import { LoginScreen } from "./routes/login"
import { OverviewScreen } from "./routes/overview"

const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      // Spec §5: polling, paused when the tab is hidden. A dashboard nobody is
      // looking at should not keep a gateway busy.
      refetchOnWindowFocus: true,
      refetchIntervalInBackground: false,
      retry: (count, err) =>
        // A 401 is not a transient failure. Retrying it three times delays the
        // login screen for no reason.
        !(err instanceof ApiError && err.status === 401) && count < 2,
    },
  },
})

type AuthStatus = { authenticated: boolean; csrf_token?: string }

function Shell() {
  const [authed, setAuthed] = useState<boolean | null>(null)

  const status = useQuery({
    queryKey: ["auth-status"],
    queryFn: () => api.get<AuthStatus>("/api/auth/status"),
  })

  useEffect(() => {
    if (!status.data) return
    setAuthed(status.data.authenticated)
    if (status.data.csrf_token) setCsrfToken(status.data.csrf_token)
  }, [status.data])

  useEffect(
    () =>
      onUnauthorized(() => {
        // Any call anywhere can discover the session is gone. One listener
        // beats every screen handling it.
        setAuthed(false)
        queryClient.clear()
      }),
    [],
  )

  if (authed === null) return null
  if (!authed) {
    return <LoginScreen onAuthenticated={() => void status.refetch()} />
  }
  return (
    <SidebarLayout
      nav={[
        {
          items: [
            { label: "Overview", href: "/" },
            { label: "Requests", href: "/requests" },
            { label: "Catalog", href: "/catalog" },
            { label: "Playground", href: "/playground" },
            { label: "Settings", href: "/settings" },
          ],
        },
      ]}
      showThemeSwitcher
    >
      <OverviewScreen />
    </SidebarLayout>
  )
}

export function App() {
  return (
    <ThemeProvider config={themeConfig}>
      <QueryClientProvider client={queryClient}>
        <Shell />
      </QueryClientProvider>
    </ThemeProvider>
  )
}

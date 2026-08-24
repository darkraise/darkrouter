import { useEffect, useState } from "react"
import { QueryClient, QueryClientProvider, useQuery } from "@tanstack/react-query"
import { ThemeProvider } from "darkraise-ui/theme"
import { themeConfig } from "./theme.config"
import { api, onUnauthorized, setCsrfToken } from "./lib/api"
import { ApiError } from "./lib/api"
import { RouterProvider } from "@tanstack/react-router"
import { LoginScreen } from "./routes/login"
import { router } from "./lib/router"

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
  // The shell is the root route's component rather than a wrapper here, so
  // that darkraise-ui's sidebar has the router context its adapter needs.
  return <RouterProvider router={router} />
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

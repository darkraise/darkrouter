import { useState } from "react"
import { useNavigate, useSearch } from "@tanstack/react-router"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "darkraise-ui/components/tabs"
import { ChatMode } from "./chat/chat-mode"
import { Compare } from "./compare"
import { AuxMode } from "./aux/aux-mode"
import { emptyConfig, type PlaygroundConfig } from "./config"
import { initialMode, readMode, rememberMode } from "./mode"

/**
 * The playground, as three surfaces rather than a mode with tabs inside it.
 *
 * Lab is gone, and its two halves went to the two places that needed them.
 * Single's request pane is Chat's, because tuning a request and keeping the
 * conversation it produced were never two activities. Compare is a surface in
 * its own right and now says so rather than hiding one level down. Token Count
 * lives with the other single-request Auxiliary tools, where operators can
 * measure a prompt before sending it.
 *
 * The chosen surface is remembered per operator and carried in the URL, so a
 * shared link opens where its sender meant rather than where its reader last
 * was.
 */
export function PlaygroundScreen() {
  const search = useSearch({ strict: false })
  const navigate = useNavigate()
  // The URL is the surface, rather than a copy of it kept in state. The
  // router does not remount a route when only its search changes, so a copy
  // had to be resynced on every Back -- and Back is the browser restoring a
  // location, not the operator picking a surface, which meant that resync had
  // to be careful to neither write the preference nor navigate. Reading the
  // location directly leaves nothing to resync and nothing to get wrong.
  const mode = initialMode(search)
  // Compare's request settings live here rather than inside it, so switching
  // to Auxiliary and back does not silently reset the system prompt every
  // column was being compared under.
  const [compareConfig, setCompareConfig] = useState<PlaygroundConfig>(emptyConfig)

  function choose(next: string) {
    const picked = readMode(next)
    if (picked === undefined || picked === mode) return
    rememberMode(picked)
    void navigate({ to: "/playground", search: (prev) => ({ ...prev, mode: picked }) })
  }

  return (
    <div className="-m-6 flex h-[calc(100%+3rem)] min-h-0 flex-col">
      {/* Centred, and a tab strip rather than a toggle: these are three views
          of one screen, not a setting on it. The page's name and purpose are
          in the app header, so nothing competes with it for the top line. */}
      <Tabs value={mode} onValueChange={choose} className="flex min-h-0 flex-1 flex-col">
        <div className="flex justify-center px-6 pt-4 pb-2">
          <TabsList aria-label="Playground surface">
            <TabsTrigger value="chat">Chat</TabsTrigger>
            <TabsTrigger value="compare">Compare</TabsTrigger>
            <TabsTrigger value="auxiliary">Auxiliary</TabsTrigger>
          </TabsList>
        </div>

        <TabsContent
          value="chat"
          forceMount
          className="flex min-h-0 flex-1 flex-col data-[state=inactive]:hidden"
        >
          <ChatMode active={mode === "chat"} />
        </TabsContent>

        <TabsContent
          value="compare"
          forceMount
          className="flex min-h-0 flex-1 flex-col data-[state=inactive]:hidden"
        >
          <Compare config={compareConfig} onConfigChange={setCompareConfig} active={mode === "compare"} />
        </TabsContent>

        <TabsContent
          value="auxiliary"
          forceMount
          className="flex min-h-0 flex-1 flex-col data-[state=inactive]:hidden"
        >
          <AuxMode active={mode === "auxiliary"} />
        </TabsContent>
      </Tabs>
    </div>
  )
}

import { useEffect, useState } from "react"
import { useNavigate, useSearch } from "@tanstack/react-router"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "darkraise-ui/components/tabs"
import { ChatMode } from "./chat/chat-mode"
import { Compare } from "./compare"
import { AuxMode } from "./aux/aux-mode"
import { emptyConfig, type PlaygroundConfig } from "./config"
import { initialMode, readMode, rememberMode, type PlaygroundMode } from "./mode"

/**
 * The playground, as three surfaces rather than a mode with tabs inside it.
 *
 * Lab is gone, and its two halves went to the two places that needed them.
 * Single's request pane is Chat's, because tuning a request and keeping the
 * conversation it produced were never two activities. Compare is a surface in
 * its own right and now says so rather than hiding one level down. Count went
 * with Lab: a token count is a reading the request log already carries for
 * every request that was actually sent.
 *
 * The chosen surface is remembered per operator and carried in the URL, so a
 * shared link opens where its sender meant rather than where its reader last
 * was.
 */
export function PlaygroundScreen() {
  const search = useSearch({ strict: false })
  const navigate = useNavigate()
  const [mode, setMode] = useState<PlaygroundMode>(() => initialMode(search))
  // Compare's request settings live here rather than inside it, so switching
  // to Auxiliary and back does not silently reset the system prompt every
  // column was being compared under.
  const [compareConfig, setCompareConfig] = useState<PlaygroundConfig>(emptyConfig)

  function choose(next: string) {
    const picked = readMode(next)
    if (picked === undefined || picked === mode) return
    setMode(picked)
    rememberMode(picked)
    void navigate({ to: "/playground", search: (prev) => ({ ...prev, mode: picked }) })
  }

  // The seed above runs once, and the router does not remount a route when
  // only its search changes -- so without this, Back after a switch moves the
  // URL to ?mode=compare and leaves Chat on screen.
  //
  // It sets the mode rather than routing through choose(). Back is the
  // browser restoring a location, not the operator picking a surface, so it
  // must neither write the preference nor navigate -- and navigating would
  // push ?mode= back onto a bare /playground and undo the Back that just
  // happened.
  useEffect(() => {
    setMode(initialMode(search))
    // search is a fresh object each render; its two fields are what matter.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [search.mode, search.seed])

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
          className="flex min-h-0 flex-1 flex-col data-[state=inactive]:hidden"
        >
          <ChatMode />
        </TabsContent>

        <TabsContent
          value="compare"
          className="flex min-h-0 flex-1 flex-col data-[state=inactive]:hidden"
        >
          <Compare config={compareConfig} onConfigChange={setCompareConfig} />
        </TabsContent>

        <TabsContent
          value="auxiliary"
          className="flex min-h-0 flex-1 flex-col data-[state=inactive]:hidden"
        >
          <AuxMode />
        </TabsContent>
      </Tabs>
    </div>
  )
}

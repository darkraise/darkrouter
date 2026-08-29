import { useState } from "react"
import { PageHeader } from "darkraise-ui/layout"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "darkraise-ui/components/tabs"
import { ChatTab } from "./chat-tab"
import { Compare } from "./compare"
import { AuxPanels, Count } from "./aux-panels"
import { ConfigPane } from "./config-pane"
import { emptyConfig, type PlaygroundConfig } from "./config"
import { MetricsStrip, NO_METRICS, hasReadings, type StreamMetrics } from "./metrics"

/**
 * The playground, as one instrument rather than four forms.
 *
 * The request settings and the last run's readings live here, above the tabs,
 * because they belong to the request rather than to whichever surface sent it:
 * a system prompt typed on Chat used to be invisible to Compare and lost on a
 * tab switch, and the timing that matters most for a streaming surface — how
 * long until the first token — was not shown at all.
 *
 * Auxiliary and Count keep their own controls. They send embeddings, images
 * and token counts, which take different inputs from a chat turn, and pointing
 * a chat model picker at them would be a control that lies.
 */
export function PlaygroundScreen() {
  const [config, setConfig] = useState<PlaygroundConfig>(emptyConfig)
  const [metrics, setMetrics] = useState<StreamMetrics>(NO_METRICS)
  const [tab, setTab] = useState("chat")
  const sends = tab === "chat" || tab === "compare"

  return (
    <div className="-m-6 flex h-[calc(100%+3rem)] min-h-0 flex-col">
      <PageHeader
        title="Playground"
        description="Send a real request, and see what it cost"
      />
      <Tabs value={tab} onValueChange={setTab} className="flex min-h-0 flex-1 flex-col">
      {/* Sized to its tabs: stretched across the page it reads as an empty
          band with four words adrift in it. */}
      <TabsList className="mx-6 w-fit">
        <TabsTrigger value="chat">Chat</TabsTrigger>
        <TabsTrigger value="compare">Compare</TabsTrigger>
        <TabsTrigger value="auxiliary">Auxiliary</TabsTrigger>
        <TabsTrigger value="count">Count</TabsTrigger>
      </TabsList>

      {/* Only once a run has produced a reading: a row of em dashes above an
          empty chat is furniture, and it pushed the conversation down the
          page before there was anything to measure. */}
      {sends && hasReadings(metrics) && <MetricsStrip metrics={metrics} />}

      <div className="flex min-h-0 flex-1 flex-col lg:flex-row">
        <div className="flex min-h-0 min-w-0 flex-1 flex-col">
          <TabsContent value="chat" className="flex min-h-0 flex-1 flex-col data-[state=inactive]:hidden">
            <ChatTab config={config} onConfigChange={setConfig} onMetrics={setMetrics} />
          </TabsContent>
          <TabsContent value="compare" className="flex min-h-0 flex-1 flex-col data-[state=inactive]:hidden">
            <Compare config={config} />
          </TabsContent>
          <TabsContent value="auxiliary" className="flex min-h-0 flex-1 flex-col data-[state=inactive]:hidden">
            <AuxPanels />
          </TabsContent>
          <TabsContent value="count" className="flex min-h-0 flex-1 flex-col data-[state=inactive]:hidden">
            <Count />
          </TabsContent>
        </div>
        {sends && <ConfigPane config={config} onChange={setConfig} />}
      </div>
      </Tabs>
    </div>
  )
}

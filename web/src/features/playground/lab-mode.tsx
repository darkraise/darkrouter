import { useState } from "react"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "darkraise-ui/components/tabs"
import { ChatTab } from "./chat-tab"
import { Compare } from "./compare"
import { AuxPanels, Count } from "./aux-panels"
import { ConfigPane } from "./config-pane/config-pane"
import type { PlaygroundConfig } from "./config"
import { MetricsStrip, NO_METRICS, type StreamMetrics } from "./metrics"

/**
 * The instrument: four surfaces that send a real request, one config pane
 * describing that request, and the readings the last run produced.
 *
 * The request settings are one object shared by all four surfaces, because
 * they belong to the request rather than to whichever surface sent it: a
 * system prompt typed on Single used to be invisible to Compare and lost on a
 * tab switch. They are held by the screen above rather than here, so that
 * Chat mode can hand a conversation's configuration across without routing it
 * through storage or the URL.
 *
 * Auxiliary and Count keep their own controls. They send embeddings, images
 * and token counts, which take different inputs from a chat turn, and pointing
 * a chat model picker at them would be a control that lies.
 */
export function LabMode({
  config,
  onConfigChange,
}: {
  config: PlaygroundConfig
  onConfigChange: (next: PlaygroundConfig) => void
}) {
  const [metrics, setMetrics] = useState<StreamMetrics>(NO_METRICS)
  const [tab, setTab] = useState("single")
  const sends = tab === "single" || tab === "compare"

  return (
    <Tabs value={tab} onValueChange={setTab} className="flex min-h-0 flex-1 flex-col">
      {/* Sized to its tabs: stretched across the page it reads as an empty
          band with four words adrift in it. */}
      <TabsList className="mx-6 w-fit">
        <TabsTrigger value="single">Single</TabsTrigger>
        <TabsTrigger value="compare">Compare</TabsTrigger>
        <TabsTrigger value="auxiliary">Auxiliary</TabsTrigger>
        <TabsTrigger value="count">Count</TabsTrigger>
      </TabsList>

      {/* The height is reserved from the start, em dashes and all. This is a
          mode whose whole purpose is measurement, and a strip that appeared
          after the first run would shift the transcript down at exactly the
          moment the operator started reading it. */}
      {sends && <MetricsStrip metrics={metrics} />}

      <div className="flex min-h-0 flex-1 flex-col lg:flex-row">
        <div className="flex min-h-0 min-w-0 flex-1 flex-col">
          <TabsContent value="single" className="flex min-h-0 flex-1 flex-col data-[state=inactive]:hidden">
            <ChatTab config={config} onConfigChange={onConfigChange} onMetrics={setMetrics} />
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
        {sends && <ConfigPane config={config} onChange={onConfigChange} />}
      </div>
    </Tabs>
  )
}

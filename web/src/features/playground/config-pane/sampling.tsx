import { Label, Switch, Textarea } from "darkraise-ui"
import { GatedField, retainedValueClass } from "./gated-field"
import { NumberBox } from "../../shell/number-box"
import { reasonFor } from "../dialect-support"
import type { PlaygroundConfig } from "../config"

export function Sampling({
  config,
  onChange,
}: {
  config: PlaygroundConfig
  onChange: (next: PlaygroundConfig) => void
}) {
  const set = <K extends keyof PlaygroundConfig>(key: K, value: PlaygroundConfig[K]) =>
    onChange({ ...config, [key]: value })
  const why = (control: Parameters<typeof reasonFor>[1]) => reasonFor(config.dialect, control)

  return (
    <div className="flex flex-col gap-4">
      <div className="flex gap-2">
        <GatedField reason={why("temperature")}>
          <Label htmlFor="pg-temp">Temperature</Label>
          <NumberBox
            id="pg-temp"
            placeholder="default" step={0.1}
            disabled={why("temperature") !== null}
            retainValue
            value={config.temperature}
            onChange={(next) => set("temperature", next)}
          />
        </GatedField>
        <GatedField reason={why("maxTokens")}>
          <Label htmlFor="pg-max">Max tokens</Label>
          <NumberBox
            id="pg-max"
            placeholder="default" precision={0}
            disabled={why("maxTokens") !== null}
            retainValue
            value={config.maxTokens}
            onChange={(next) => set("maxTokens", next)}
          />
        </GatedField>
      </div>

      <div className="flex gap-2">
        <GatedField reason={why("topP")}>
          <Label htmlFor="pg-topp">Top P</Label>
          <NumberBox
            id="pg-topp"
            placeholder="default" step={0.01}
            disabled={why("topP") !== null}
            retainValue
            value={config.topP}
            onChange={(next) => set("topP", next)}
          />
        </GatedField>
        <GatedField reason={why("topK")}>
          <Label htmlFor="pg-topk">Top K</Label>
          <NumberBox
            id="pg-topk"
            placeholder="default" precision={0}
            disabled={why("topK") !== null}
            retainValue
            value={config.topK}
            onChange={(next) => set("topK", next)}
          />
        </GatedField>
      </div>

      <GatedField reason={why("stop")}>
        <Label htmlFor="pg-stop">Stop sequences</Label>
        <Textarea
          id="pg-stop" rows={2} placeholder="one per line"
          disabled={why("stop") !== null}
          value={config.stopRaw}
          onChange={(e) => set("stopRaw", e.target.value)}
          className={`font-mono text-sm ${retainedValueClass(config.stopRaw)}`}
        />
      </GatedField>

      {/* Named through htmlFor rather than by wrapping. Switch renders a
          `role="switch"` button, and a button takes its accessible name from
          aria-label or its own subtree — never from an enclosing label — so
          the wrapped version announced as "switch, off" with no indication of
          what it switched. */}
      <div className="flex items-center gap-2">
        <Switch
          id="pg-stream"
          checked={config.stream}
          onCheckedChange={(next) => set("stream", next)}
        />
        <Label htmlFor="pg-stream">Stream the reply</Label>
      </div>
    </div>
  )
}

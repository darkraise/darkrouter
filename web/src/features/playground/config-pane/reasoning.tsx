import {
  Input, Label, Select, SelectContent, SelectItem, SelectTrigger, SelectValue,
} from "darkraise-ui"
import { GatedField } from "./gated-field"
import { reasonFor } from "../dialect-support"
import type { PlaygroundConfig } from "../config"

const EFFORTS = ["low", "medium", "high"]

/**
 * One capability, two spellings.
 *
 * ir.Reasoning holds an effort tier and a token budget because the wires
 * disagree about which to take, not because they are different settings. Shown
 * under one heading so the operator learns they are the same idea rather than
 * hunting for the one their dialect happens to use.
 */
export function Reasoning({
  config,
  onChange,
}: {
  config: PlaygroundConfig
  onChange: (next: PlaygroundConfig) => void
}) {
  const set = <K extends keyof PlaygroundConfig>(key: K, value: PlaygroundConfig[K]) =>
    onChange({ ...config, [key]: value })
  const effortWhy = reasonFor(config.dialect, "reasoningEffort")
  const budgetWhy = reasonFor(config.dialect, "reasoningBudget")

  return (
    <div className="flex flex-col gap-4">
      <GatedField reason={effortWhy}>
        <Label htmlFor="pg-effort">Effort</Label>
        <Select
          value={config.reasoningEffort}
          onValueChange={(v) => set("reasoningEffort", v)}
          disabled={effortWhy !== null}
        >
          {/* Label association only, no aria-label: the existing dialect
              select in this pane pairs Label htmlFor with SelectTrigger id,
              and two labelling mechanisms on one control is one too many. */}
          <SelectTrigger id="pg-effort">
            <SelectValue placeholder="default" />
          </SelectTrigger>
          <SelectContent>
            {EFFORTS.map((e) => (
              <SelectItem key={e} value={e}>{e}</SelectItem>
            ))}
          </SelectContent>
        </Select>
      </GatedField>

      <GatedField reason={budgetWhy}>
        <Label htmlFor="pg-budget">Budget</Label>
        <Input
          id="pg-budget" type="number" placeholder="tokens"
          disabled={budgetWhy !== null}
          value={config.reasoningBudget}
          onChange={(e) => set("reasoningBudget", e.target.value)}
        />
      </GatedField>
    </div>
  )
}

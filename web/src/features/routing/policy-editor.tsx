import { useState, type ChangeEvent } from "react"
import { Badge, Button, Card, Input, Label } from "darkraise-ui"
import { api } from "../../lib/api"
import { useApiMutation } from "../../lib/mutations"
import { keys, usePolicy } from "../../lib/queries"
import type { PolicyBlock } from "../../lib/api-types"

/** The two timeout keys a reload cannot apply: both configure the one shared
 *  http.Transport built at startup, so `PUT /api/policy` refuses a write that
 *  touches either. */
export const RESTART_ONLY_TIMEOUTS = ["connect", "first_byte"] as const

type Draft = {
  tripAfter: string
  cooldownMax: string
  maxAttempts: string
  total: string
  idle: string
}

function toDraft(p: PolicyBlock): Draft {
  return {
    tripAfter: p.cooldown.trip_after !== undefined ? String(p.cooldown.trip_after) : "",
    cooldownMax: p.cooldown.max,
    maxAttempts: String(p.retry.max_attempts),
    total: p.timeout.total,
    idle: p.timeout.idle,
  }
}

type PolicyWrite = {
  cooldown: { trip_after?: number; max: string }
  retry: { max_attempts: number }
  timeout: { total: string; idle: string }
}

function PolicyForm({ policy }: { policy: PolicyBlock }) {
  const [draft, setDraft] = useState<Draft>(() => toDraft(policy))

  const save = useApiMutation({
    mutationFn: (body: PolicyWrite) => api.put("/api/policy", body),
    success: "Policy saved",
    invalidates: [keys.policy, keys.config],
  })

  function set(field: keyof Draft) {
    return (e: ChangeEvent<HTMLInputElement>) =>
      setDraft((d) => ({ ...d, [field]: e.target.value }))
  }

  function submit() {
    // connect and first_byte never enter this object — the two restart-only
    // keys are omitted rather than sent and refused.
    save.mutate({
      cooldown: {
        max: draft.cooldownMax,
        ...(draft.tripAfter.trim() !== "" ? { trip_after: Number(draft.tripAfter) } : {}),
      },
      retry: { max_attempts: Number(draft.maxAttempts) },
      timeout: { total: draft.total, idle: draft.idle },
    })
  }

  return (
    <Card className="p-4">
      <h2 className="mb-3 text-sm font-medium">Policy</h2>
      <div className="grid grid-cols-2 gap-4 sm:grid-cols-3">
        <div className="flex flex-col gap-1.5">
          <Label htmlFor="policy-trip-after">Cooldown trip after</Label>
          <Input
            id="policy-trip-after"
            type="number"
            value={draft.tripAfter}
            onChange={set("tripAfter")}
          />
        </div>
        <div className="flex flex-col gap-1.5">
          <Label htmlFor="policy-cooldown-max">Cooldown max</Label>
          <Input
            id="policy-cooldown-max"
            value={draft.cooldownMax}
            onChange={set("cooldownMax")}
            className="font-mono text-sm"
          />
        </div>
        <div className="flex flex-col gap-1.5">
          <Label htmlFor="policy-max-attempts">Retry max attempts</Label>
          <Input
            id="policy-max-attempts"
            type="number"
            value={draft.maxAttempts}
            onChange={set("maxAttempts")}
          />
        </div>
        {/* total and idle are read per request, so a reload picks them up —
            these two are ordinary, hot-reloadable inputs. */}
        <div className="flex flex-col gap-1.5">
          <Label htmlFor="policy-total">Total timeout</Label>
          <Input
            id="policy-total"
            value={draft.total}
            onChange={set("total")}
            className="font-mono text-sm"
          />
        </div>
        <div className="flex flex-col gap-1.5">
          <Label htmlFor="policy-idle">Idle timeout</Label>
          <Input
            id="policy-idle"
            value={draft.idle}
            onChange={set("idle")}
            className="font-mono text-sm"
          />
        </div>
        {/* connect and first_byte configure the one shared transport built at
            startup: no reload can apply them, so they are shown disabled with
            a restart badge rather than as fields that accept a value and
            throw it away. */}
        <div className="flex flex-col gap-1.5">
          <div className="flex items-center gap-2">
            <Label htmlFor="policy-connect">Connect timeout</Label>
            <Badge variant="secondary">restart</Badge>
          </div>
          <Input
            id="policy-connect"
            value={policy.timeout.connect}
            disabled
            className="font-mono text-sm"
          />
        </div>
        <div className="flex flex-col gap-1.5">
          <div className="flex items-center gap-2">
            <Label htmlFor="policy-first-byte">First byte timeout</Label>
            <Badge variant="secondary">restart</Badge>
          </div>
          <Input
            id="policy-first-byte"
            value={policy.timeout.first_byte}
            disabled
            className="font-mono text-sm"
          />
        </div>
      </div>
      <div className="mt-4">
        <Button size="sm" onClick={submit}>
          Save
        </Button>
      </div>
    </Card>
  )
}

export function PolicyEditor() {
  const policy = usePolicy()
  if (!policy.data) return null
  return <PolicyForm policy={policy.data} />
}

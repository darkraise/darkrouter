import type { FailoverRow } from "../../lib/api-types"

/** "alias → provider/model": what was asked for, and who finally served it. */
export function failoverLabel(row: FailoverRow): string {
  const asked = row.alias || row.final_model
  return `${asked} → ${row.final_provider_id}/${row.final_model}`
}

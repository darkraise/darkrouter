import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "darkraise-ui"

/** A combobox over a filter's live values. Radix's Select cannot carry an
 *  empty-string item value, so "any" stands in for "no filter" on the wire
 *  between this component and the caller.
 *
 *  The label is the control's name and stays on it once a value is picked:
 *  a row of bare values, "error" beside "api_error", left the reader to
 *  guess which select was Status, and a screen reader heard no name at all. */
export function FilterSelect({
  label,
  value,
  options,
  onChange,
}: {
  label: string
  value: string
  options: string[]
  onChange: (v: string) => void
}) {
  return (
    <Select value={value === "" ? "any" : value} onValueChange={(v) => onChange(v === "any" ? "" : v)}>
      <SelectTrigger className="w-auto min-w-36" aria-label={label}>
        <SelectValue placeholder={label}>
          {value === "" ? `Any ${label.toLowerCase()}` : `${label}: ${value}`}
        </SelectValue>
      </SelectTrigger>
      <SelectContent>
        <SelectItem value="any">Any {label.toLowerCase()}</SelectItem>
        {options.map((o) => (
          <SelectItem key={o} value={o}>
            {o}
          </SelectItem>
        ))}
      </SelectContent>
    </Select>
  )
}

import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "darkraise-ui"

/** A combobox over a filter's live values. Radix's Select cannot carry an
 *  empty-string item value, so "any" stands in for "no filter" on the wire
 *  between this component and the caller. */
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
      <SelectTrigger className="w-36">
        <SelectValue placeholder={label} />
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

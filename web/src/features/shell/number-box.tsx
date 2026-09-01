import {
  NumberInput,
  NumberInputControl,
  NumberInputDecrementTrigger,
  NumberInputField,
  NumberInputIncrementTrigger,
  NumberInputSuffix,
  NumberInputTriggerGroup,
} from "darkraise-ui"

/**
 * A number field over a string.
 *
 * Every numeric setting in the console is held as a string, because an empty
 * box and a zero are different settings and a number cannot hold both — see
 * `playground/config.ts`, and `provider-settings-dialog.tsx` for what it costs
 * when the distinction is lost. darkraise-ui's `NumberInput` is a number
 * component, so the two meet here rather than at each call site.
 *
 * The mapping is `NaN` in both directions: the component formats `NaN` as an
 * empty field, and reports `{ value: "" }` when the box is cleared. It also
 * hands back the *raw* typed string rather than a reformatted one, so a
 * half-written `0.` survives until the operator finishes the number.
 *
 * Written once because `Number("")` is `0`. A call site that reached for
 * `Number(value)` without the empty check would quietly turn "unset" into a
 * real zero — the exact bug `settingsPatch` already guards against — and there
 * is no version of that mistake that announces itself.
 */
export function NumberBox({
  id,
  value,
  onChange,
  placeholder,
  disabled = false,
  retainValue = false,
  suffix,
  step,
  precision,
  className,
}: {
  id?: string
  /** The stored string. "" means unset, and stays distinct from "0". */
  value: string
  /** The raw string the operator has typed, never a coerced number. */
  onChange: (next: string) => void
  placeholder?: string
  disabled?: boolean
  /** Hold a disabled control's value at body contrast rather than dimming it
   *  to placeholder grey. See `retainedValueClass` for the reasoning; the
   *  border dims instead so the field still reads as gated. */
  retainValue?: boolean
  /** A unit that stays put. A placeholder saying "tokens" vanishes at the
   *  first keystroke, which is when the unit starts mattering. */
  suffix?: string
  step?: number
  /** 0 for a count. Left unset for a rate, so a typed `0.7` is not reformatted
   *  into `0.70` the moment the field loses focus. */
  precision?: number
  className?: string
}) {
  const dimmed = disabled && retainValue
  return (
    <NumberInput
      // No min or max: these fields never had bounds, and adding them would
      // let a blur silently clamp a value a preset had stored.
      value={value === "" ? Number.NaN : Number(value)}
      onValueChange={(d) => onChange(d.value)}
      disabled={disabled}
      step={step}
      precision={precision}
      className={className}
    >
      <NumberInputControl
        className={dimmed ? "border-[hsl(var(--input)/0.5)]" : undefined}
      >
        <NumberInputField
          id={id}
          placeholder={placeholder}
          className={dimmed ? "disabled:opacity-100" : undefined}
        />
        {suffix ? <NumberInputSuffix>{suffix}</NumberInputSuffix> : null}
        {/* Dropped while gated rather than shown inert: a stepper that cannot
            step is two more controls answering nothing, on a field whose
            reason for being disabled is already written underneath it. */}
        {disabled ? null : (
          <NumberInputTriggerGroup>
            <NumberInputIncrementTrigger />
            <NumberInputDecrementTrigger />
          </NumberInputTriggerGroup>
        )}
      </NumberInputControl>
    </NumberInput>
  )
}

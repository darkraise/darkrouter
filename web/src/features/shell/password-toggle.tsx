import { PasswordInputIndicator, PasswordInputVisibilityTrigger } from "darkraise-ui"
import { Eye, EyeOff } from "lucide-react"

/** The reveal button for every password field. The library's trigger draws
 *  nothing of its own, so without an indicator it is an invisible, focusable
 *  square; one component keeps the four fields that use it looking alike. */
export function PasswordToggle() {
  return (
    <PasswordInputVisibilityTrigger>
      <PasswordInputIndicator
        visible={<EyeOff className="size-4" aria-hidden="true" />}
        hidden={<Eye className="size-4" aria-hidden="true" />}
      />
    </PasswordInputVisibilityTrigger>
  )
}

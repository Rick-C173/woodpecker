import * as React from "react"
import { cn } from "@/lib/utils"

function Textarea({ className, ...props }: React.ComponentProps<"textarea">) {
  return (
    <textarea
      data-slot="textarea"
      className={cn(
        "flex field-sizing-content min-h-16 w-full rounded-[var(--radius)] border border-[var(--border)] bg-[var(--card)] px-3 py-2 text-sm shadow-[var(--shadow)] transition-[color,box-shadow,border-color] outline-none placeholder:text-[var(--faint)] focus-visible:border-[var(--border-strong)] focus-visible:ring-[3px] focus-visible:ring-[var(--primary-soft)] disabled:cursor-not-allowed disabled:opacity-50 aria-invalid:border-[var(--destructive)]",
        className
      )}
      {...props}
    />
  )
}

export { Textarea }

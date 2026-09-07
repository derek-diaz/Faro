import type { ComponentProps } from "react";
import { cn } from "../../lib/utils";

/** Native selection preserves form submission and platform keyboard behavior. */
export function NativeSelect({ className, ...props }: ComponentProps<"select">) {
  return <select data-slot="native-select" className={cn("faro-select", className)} {...props} />;
}

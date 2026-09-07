import { Checkbox as CheckboxPrimitive } from "@base-ui/react/checkbox";
import { Check } from "lucide-react";
import { cn } from "../../lib/utils";

export function Checkbox({ className, ...props }: CheckboxPrimitive.Root.Props) {
  return <CheckboxPrimitive.Root data-slot="checkbox" className={cn("faro-checkbox", className)} {...props}>
    <CheckboxPrimitive.Indicator><Check size={13} /></CheckboxPrimitive.Indicator>
  </CheckboxPrimitive.Root>;
}

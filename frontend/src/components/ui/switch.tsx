import { Switch as SwitchPrimitive } from "@base-ui/react/switch";
import { cn } from "../../lib/utils";

export function Switch({ className, ...props }: SwitchPrimitive.Root.Props) {
  return <SwitchPrimitive.Root data-slot="switch" className={cn("faro-switch", className)} {...props}>
    <SwitchPrimitive.Thumb data-slot="switch-thumb" className="faro-switch-thumb" />
  </SwitchPrimitive.Root>;
}

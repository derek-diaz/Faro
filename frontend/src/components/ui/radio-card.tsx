import { Radio } from "@base-ui/react/radio";
import { RadioGroup } from "@base-ui/react/radio-group";
import { Check } from "lucide-react";
import { cn } from "../../lib/utils";

export { RadioGroup };

export function RadioCard({ className, children, ...props }: Radio.Root.Props<string>) {
  return <Radio.Root render={<button type="button" />} nativeButton className={cn("faro-radio-card", className)} {...props}>
    {children}
    <Radio.Indicator className="faro-radio-card-indicator" keepMounted aria-hidden="true"><Check size={16} /></Radio.Indicator>
  </Radio.Root>;
}

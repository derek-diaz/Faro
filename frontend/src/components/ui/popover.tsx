import { Popover as PopoverPrimitive } from "@base-ui/react/popover";
import { cn } from "@/lib/utils";

export const Popover = PopoverPrimitive.Root;
export const PopoverTrigger = PopoverPrimitive.Trigger;
export const PopoverClose = PopoverPrimitive.Close;

export function PopoverContent({ className, align = "end", sideOffset = 8, ...props }:
  PopoverPrimitive.Popup.Props & Pick<PopoverPrimitive.Positioner.Props, "align" | "sideOffset">) {
  return <PopoverPrimitive.Portal>
    <PopoverPrimitive.Positioner className="z-[120] outline-none" align={align} sideOffset={sideOffset} collisionPadding={12}>
      <PopoverPrimitive.Popup className={cn("faro-popover", className)} {...props} />
    </PopoverPrimitive.Positioner>
  </PopoverPrimitive.Portal>;
}

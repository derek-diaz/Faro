import type { ComponentProps } from "react";
import { cn } from "../../lib/utils";

/** Presentation only; row models and sorting remain owned by TanStack Table. */
export function Table({ className, ...props }: ComponentProps<"table">) {
  return <table className={cn("faro-table", className)} {...props} />;
}

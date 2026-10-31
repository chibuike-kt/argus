import type { ComponentProps } from "react";
import * as Tabs from "@radix-ui/react-tabs";
import { cn } from "@/lib/cn";

export const TabsRoot = Tabs.Root;
export const TabsContent = Tabs.Content;

export function TabsList({ className, ...props }: ComponentProps<typeof Tabs.List>) {
  return (
    <Tabs.List
      className={cn("glass inline-flex items-center gap-1 rounded-full p-1", className)}
      {...props}
    />
  );
}

export function TabsTrigger({ className, ...props }: ComponentProps<typeof Tabs.Trigger>) {
  return (
    <Tabs.Trigger
      className={cn(
        "focus-ring inline-flex h-8 items-center rounded-full px-4 text-[13px] font-medium text-fg-muted transition-colors hover:text-fg data-[state=active]:bg-chrome-active data-[state=active]:text-chrome-active-foreground",
        className,
      )}
      {...props}
    />
  );
}

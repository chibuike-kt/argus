import { ChartToolbar } from "@/components/workspace/chart-toolbar";
import { ToolRail } from "@/components/workspace/tool-rail";
import { RightPanel } from "@/components/workspace/right-panel";
import { OtherSetupsStrip } from "@/components/workspace/other-setups-strip";
import { ArgusActivityProvider } from "@/components/workspace/argus-activity-feed";
import { ChartSurface } from "@/components/chart/chart-surface";
import { DrawingProvider } from "@/components/chart/drawing-context";

// The analysis workspace: a single chart toolbar on top, the drawing-tool rail
// running full height on the left (collapsible), a center column (chart,
// then a compact "other setups" strip under it -- the strip sits under the
// chart only, not edge to edge under the rail/right panel too), and the
// markets / symbol panel on the right, running the full height of the row
// (independent of the strip) and user-resizable. The DrawingProvider shares
// the active tool and drawn shapes between the rail and the chart;
// ArgusActivityProvider narrates ARGUS's own analysis steps as a toast
// feed, so the workspace feels like ARGUS is actively working it, not a
// static read-out.
export function AnalysisPreview() {
  return (
    <DrawingProvider>
      <ArgusActivityProvider>
        <div className="flex h-full flex-col gap-1.5 p-1.5">
          <ChartToolbar />

          <div className="flex min-h-0 flex-1 gap-1.5">
            <ToolRail />

            <div
              className="argus-in flex min-w-0 flex-1 flex-col gap-1.5"
              style={{ "--argus-in-delay": "80ms" } as React.CSSProperties}
            >
              <div className="min-h-0 flex-1">
                <ChartSurface />
              </div>
              <OtherSetupsStrip />
            </div>

            <RightPanel />
          </div>
        </div>
      </ArgusActivityProvider>
    </DrawingProvider>
  );
}

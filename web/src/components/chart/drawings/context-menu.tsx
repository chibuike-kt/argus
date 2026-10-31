import { useEffect, useRef } from "react";
import { Copy, Trash, StackSimple, GearSix } from "@phosphor-icons/react";

export interface DrawingContextMenuProps {
  x: number;
  y: number;
  onClone: () => void;
  onDelete: () => void;
  onBringToFront: () => void;
  onSettings: () => void;
  onClose: () => void;
}

// Right-click menu for a selected drawing: delete, clone, bring to front,
// settings. Closes on outside click or Escape.
export function DrawingContextMenu({
  x,
  y,
  onClone,
  onDelete,
  onBringToFront,
  onSettings,
  onClose,
}: DrawingContextMenuProps) {
  const ref = useRef<HTMLDivElement>(null);

  useEffect(() => {
    const onDown = (e: PointerEvent) => {
      if (ref.current && !ref.current.contains(e.target as Node)) onClose();
    };
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose();
    };
    window.addEventListener("pointerdown", onDown);
    window.addEventListener("keydown", onKey);
    return () => {
      window.removeEventListener("pointerdown", onDown);
      window.removeEventListener("keydown", onKey);
    };
  }, [onClose]);

  const item = (icon: React.ReactNode, label: string, onClick: () => void, danger = false) => (
    <button
      onClick={() => {
        onClick();
        onClose();
      }}
      className={
        "flex w-full items-center gap-2.5 rounded-md px-2.5 py-1.5 text-left text-[13px] transition-colors hover:bg-white/5 " +
        (danger ? "text-down" : "text-fg-muted hover:text-fg")
      }
    >
      {icon}
      {label}
    </button>
  );

  return (
    <div
      ref={ref}
      className="glass-strong fixed z-50 min-w-[170px] rounded-lg p-1 shadow-xl"
      style={{ left: x, top: y }}
    >
      {item(<StackSimple className="size-4" />, "Bring to front", onBringToFront)}
      {item(<Copy className="size-4" />, "Clone", onClone)}
      {item(<GearSix className="size-4" />, "Settings", onSettings)}
      {item(<Trash className="size-4" />, "Delete", onDelete, true)}
    </div>
  );
}

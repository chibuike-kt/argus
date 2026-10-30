import { clsx, type ClassValue } from "clsx";
import { twMerge } from "tailwind-merge";

// Merge conditional class names and resolve Tailwind conflicts deterministically.
export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs));
}

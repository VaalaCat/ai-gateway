import { clsx, type ClassValue } from "clsx"
import { extendTailwindMerge } from "tailwind-merge"

const tw = extendTailwindMerge({
  extend: {
    classGroups: {
      "font-size": [
        { text: ["meta", "label", "body", "heading", "display", "2xs"] },
      ],
    },
  },
})

export function cn(...inputs: ClassValue[]) {
  return tw(clsx(inputs))
}

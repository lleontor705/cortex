export interface Widget { id: number }

export function key(w: Widget): number {
  return w.id
}

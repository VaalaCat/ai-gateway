export function parseModels(models: string): string[] {
  if (!models) return [];
  try {
    const arr = JSON.parse(models);
    if (Array.isArray(arr)) return arr.map(String).filter(Boolean);
  } catch { /* not JSON */ }
  return models.split(",").map((s) => s.trim()).filter(Boolean);
}

export function serializeModels(tags: string[]): string {
  const filtered = tags.map((s) => s.trim()).filter(Boolean);
  if (filtered.length === 0) return "";
  return JSON.stringify(filtered);
}

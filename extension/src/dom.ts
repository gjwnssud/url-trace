// Tiny DOM helpers shared by popup.ts and review.ts.

export function byId<T extends HTMLElement>(id: string): T {
  const el = document.getElementById(id);
  if (!el) throw new Error(`missing #${id} in the current page`);
  return el as T;
}

/** Sets a status/error message element's text and error/info styling. */
export function setMessage(el: HTMLElement, text: string, kind: "error" | "info" = "error"): void {
  el.textContent = text;
  el.className = kind === "info" ? "message info" : "message";
}

/** Appends a warnings list below whatever setMessage() just wrote. */
export function appendWarnings(el: HTMLElement, warnings: string[]): void {
  if (warnings.length === 0) return;
  const ul = document.createElement("ul");
  ul.className = "warnings";
  for (const w of warnings) {
    const li = document.createElement("li");
    li.textContent = w;
    ul.appendChild(li);
  }
  el.appendChild(ul);
}

export function cell(text: string): HTMLTableCellElement {
  const td = document.createElement("td");
  td.textContent = text;
  return td;
}

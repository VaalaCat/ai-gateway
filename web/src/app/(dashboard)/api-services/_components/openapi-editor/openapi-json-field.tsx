"use client";

import { createContext, useContext, useEffect, useRef, useState, type ReactNode } from "react";
import { useTranslations } from "next-intl";

import { Field, FieldDescription, FieldError, FieldLabel } from "@/components/ui/field";
import { Textarea } from "@/components/ui/textarea";

import { parseJSONField, parseJSONObjectField, type JSONValue } from "./openapi-editor-state";

interface OpenAPIJSONFieldProps {
  fieldKey: string; snapshotKey: string; id: string; label: string; description?: string;
  value: JSONValue | undefined; objectOnly?: boolean; disabled: boolean;
  onChange: (value: JSONValue) => void; onValidityChange?: (fieldKey: string, valid: boolean) => void;
}

interface OpenAPIJSONFieldDraft {
  raw: string;
  error?: string;
}

interface OpenAPIEditorFieldState {
  drafts: ReadonlyMap<string, OpenAPIJSONFieldDraft>;
  setDraft: (fieldKey: string, draft: OpenAPIJSONFieldDraft) => void;
  deleteField: (fieldKey: string) => void;
  deletePrefix: (prefix: string) => void;
  movePrefix: (source: string, target: string) => void;
  parameterIDs: (scopeKey: string, count: number) => string[];
  addParameter: (scopeKey: string) => void;
  removeParameter: (scopeKey: string, index: number) => void;
}

const OpenAPIEditorFieldStateContext = createContext<OpenAPIEditorFieldState | undefined>(undefined);

function deleteDraft(drafts: Map<string, OpenAPIJSONFieldDraft>, fieldKey: string) {
  if (!drafts.has(fieldKey)) return drafts;
  const next = new Map(drafts); next.delete(fieldKey); return next;
}

function deleteDraftPrefix(drafts: Map<string, OpenAPIJSONFieldDraft>, prefix: string) {
  const next = new Map(drafts);
  for (const fieldKey of next.keys()) if (fieldKey === prefix || fieldKey.startsWith(`${prefix}:`)) next.delete(fieldKey);
  return next.size === drafts.size ? drafts : next;
}

function moveDraftPrefix(drafts: Map<string, OpenAPIJSONFieldDraft>, source: string, target: string) {
  const next = new Map(drafts);
  for (const [fieldKey, draft] of drafts) {
    if (fieldKey !== source && !fieldKey.startsWith(`${source}:`)) continue;
    next.delete(fieldKey); next.set(`${target}${fieldKey.slice(source.length)}`, draft);
  }
  return next;
}

class ParameterIdentityStore {
  private readonly ids = new Map<string, string[]>();
  private nextID = 0;

  get(scopeKey: string, count: number) {
    const current = this.ids.get(scopeKey) ?? [];
    while (current.length < count) current.push(this.create());
    if (current.length > count) current.splice(count);
    this.ids.set(scopeKey, current);
    return current;
  }

  add(scopeKey: string) {
    const current = this.ids.get(scopeKey) ?? [];
    current.push(this.create()); this.ids.set(scopeKey, current);
  }

  remove(scopeKey: string, index: number) { this.ids.get(scopeKey)?.splice(index, 1); }

  deletePrefix(prefix: string) {
    for (const scopeKey of this.ids.keys()) if (scopeKey === prefix || scopeKey.startsWith(`${prefix}:`)) this.ids.delete(scopeKey);
  }

  movePrefix(source: string, target: string) {
    for (const [scopeKey, ids] of [...this.ids]) {
      if (scopeKey !== source && !scopeKey.startsWith(`${source}:`)) continue;
      this.ids.delete(scopeKey); this.ids.set(`${target}${scopeKey.slice(source.length)}`, ids);
    }
  }

  private create() { return `parameter-${++this.nextID}`; }
}

export function OpenAPIEditorFieldStateProvider({ children, onValidityChange }: { children: ReactNode; onValidityChange: (valid: boolean) => void }) {
  const [drafts, setDrafts] = useState<Map<string, OpenAPIJSONFieldDraft>>(new Map());
  const identities = useRef(new ParameterIdentityStore()).current;
  useEffect(() => onValidityChange([...drafts.values()].every((draft) => draft.error === undefined)), [drafts, onValidityChange]);
  const value: OpenAPIEditorFieldState = {
    drafts,
    setDraft: (fieldKey, draft) => setDrafts((current) => new Map(current).set(fieldKey, draft)),
    deleteField: (fieldKey) => setDrafts((current) => deleteDraft(current, fieldKey)),
    deletePrefix: (prefix) => {
      setDrafts((current) => deleteDraftPrefix(current, prefix)); identities.deletePrefix(prefix);
    },
    movePrefix: (source, target) => {
      setDrafts((current) => moveDraftPrefix(current, source, target)); identities.movePrefix(source, target);
    },
    parameterIDs: (scopeKey, count) => identities.get(scopeKey, count),
    addParameter: (scopeKey) => identities.add(scopeKey),
    removeParameter: (scopeKey, index) => identities.remove(scopeKey, index),
  };
  return <OpenAPIEditorFieldStateContext.Provider value={value}>{children}</OpenAPIEditorFieldStateContext.Provider>;
}

export function useOpenAPIEditorFieldState() { return useContext(OpenAPIEditorFieldStateContext); }

export function OpenAPIJSONField(props: OpenAPIJSONFieldProps) {
  return <OpenAPIJSONFieldState key={`${props.fieldKey}:${props.snapshotKey}`} {...props} />;
}

function OpenAPIJSONFieldState({ fieldKey, id, label, description, value, objectOnly = false, disabled, onChange, onValidityChange }: OpenAPIJSONFieldProps) {
  const t = useTranslations("apiServices");
  const initial = value ?? (objectOnly ? {} : []);
  const [localDraft, setLocalDraft] = useState<OpenAPIJSONFieldDraft>(() => ({ raw: JSON.stringify(initial, null, 2) }));
  const editorState = useOpenAPIEditorFieldState();
  const draft = editorState?.drafts.get(fieldKey) ?? localDraft;
  const update = (next: string) => {
    const parsed = objectOnly ? parseJSONObjectField(next) : parseJSONField(next);
    const nextDraft = { raw: next, error: parsed.ok ? undefined : parsed.error };
    if (editorState) editorState.setDraft(fieldKey, nextDraft); else setLocalDraft(nextDraft);
    if (!parsed.ok) { onValidityChange?.(fieldKey, false); return; }
    onValidityChange?.(fieldKey, true);
    onChange(parsed.value);
  };
  return <Field data-invalid={draft.error ? true : undefined}>
    <FieldLabel htmlFor={id}>{label}</FieldLabel>
    {description ? <FieldDescription>{description}</FieldDescription> : null}
    <Textarea id={id} value={draft.raw} disabled={disabled} aria-invalid={draft.error ? true : undefined} onChange={(event) => update(event.target.value)} className="min-h-28 font-mono text-xs" />
    {draft.error ? <FieldError>{t(draft.error)}</FieldError> : null}
  </Field>;
}

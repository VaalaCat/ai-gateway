// web/src/components/channel/channel-form/adapter.ts
import type { ChannelTypeMeta } from "@/lib/types";
import type { ChannelForm } from "./types";

/**
 * Args for an adapter's optional test-channel hook. Shape is intentionally
 * minimal — adapters that need richer test inputs may widen via TestArgs &
 * { ... } at their own file.
 */
export interface ChannelFormTestArgs {
  id: number;
  model?: string;
  endpointType?: string;
  stream?: boolean;
  agentId?: string;
}

export interface ChannelFormTestResult {
  ok: boolean;
  status_code?: number;
  detail?: string;
  latency_ms?: number;
}

export interface ChannelFormAdapter<Entity> {
  /** List page path, used by SaveBar cancel and post-create redirect. */
  listPath: string;

  /** Form fields that must not render. */
  hiddenFields?: ReadonlySet<keyof ChannelForm>;

  /** Hint shown below the Key input. Returns undefined to render nothing. */
  keyFieldHelpText?: (entity: Entity | null) => string | undefined;

  /** Edit-mode init: backend entity → ChannelForm. */
  mapEntityToForm: (entity: Entity) => ChannelForm;

  /** Create-mode submit: ChannelForm → backend create payload. */
  buildCreatePayload: (form: ChannelForm) => unknown;

  /** Update-mode submit. rotateKey, if non-empty, is consumed by useRotateKey. */
  buildUpdatePayload: (
    form: ChannelForm,
    initial: ChannelForm,
  ) => {
    fields: Record<string, unknown>;
    rotateKey?: string;
  };

  useEntity: (id: number) => {
    data: Entity | undefined;
    isLoading: boolean;
    isError: boolean;
  };
  useCreate: () => {
    mutateAsync: (payload: unknown) => Promise<unknown>;
    isPending: boolean;
  };
  useUpdate: () => {
    mutateAsync: (args: {
      id: number;
      fields: Record<string, unknown>;
    }) => Promise<unknown>;
    isPending: boolean;
  };

  /** BYOK-only. Admin adapter must omit this property entirely. */
  useRotateKey?: () => {
    mutateAsync: (args: { id: number; key: string }) => Promise<unknown>;
    isPending: boolean;
  };

  useTypes: () => { data: ChannelTypeMeta[] | undefined };

  /** When defined, ModelsSection shows a multi-select from this catalog. */
  useModelsCatalog?: () => { data: string[] | undefined };

  /** When undefined, the Test button is hidden in edit mode. */
  useTestChannel?: () => {
    mutateAsync: (args: ChannelFormTestArgs) => Promise<ChannelFormTestResult>;
    isPending: boolean;
  };
}

import { z } from "zod";

export const routingMemberSchema = z.object({
  ref: z.string().min(1),
  priority: z.number().int().min(0).max(999),
  weight: z.number().int().min(1).max(999),
});

export const routingFormSchema = z.object({
  name: z.string().min(1).max(128).regex(/^[^,]+$/, "name_contains_comma"),
  scope: z.enum(["global", "user"]),
  user_id: z.number().int().min(0),
  members: z.array(routingMemberSchema).min(1).max(32),
  enabled: z.boolean(),
  remark: z.string().max(255),
});

export type RoutingFormValues = z.infer<typeof routingFormSchema>;

export type FormMode =
  | { kind: "new" }
  | { kind: "edit"; id: number };

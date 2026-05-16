import { z } from "zod";

export const loginSchema = z.object({
  username: z.string().min(1, "Username is required"),
  password: z.string().min(1, "Password is required"),
});

export type LoginInput = z.infer<typeof loginSchema>;

export const createUserSchema = z.object({
  username: z.string().min(2, "Username must be at least 2 characters").max(64),
  password: z.string().min(4, "Password must be at least 4 characters").max(128),
  role: z.number().int().min(1).max(2).default(1),
});

export type CreateUserInput = z.infer<typeof createUserSchema>;

export const createTokenSchema = z.object({
  user_id: z.number().int().positive(),
  name: z.string().min(1, "Name is required").max(64),
  key: z.string().max(64).optional(),
  expired_at: z.number().int().optional(),
  models: z.string().optional(),
});

export type CreateTokenInput = z.infer<typeof createTokenSchema>;

export const createChannelSchema = z.object({
  name: z.string().min(1, "Name is required").max(64),
  type: z.number().int().default(1),
  key: z.string().optional(),
  base_url: z.string().url().optional().or(z.literal("")),
  models: z.string().optional(),
  model_mapping: z.string().optional(),
  weight: z.number().int().min(0).default(1),
  priority: z.number().int().default(0),
  setting: z.string().optional(),
});

export type CreateChannelInput = z.infer<typeof createChannelSchema>;

export const createModelSchema = z.object({
  model_name: z.string().min(1, "Model name is required").max(128),
  input_price: z.number().min(0).default(0),
  output_price: z.number().min(0).default(0),
});

export type CreateModelInput = z.infer<typeof createModelSchema>;

export const createAgentSchema = z.object({
  name: z.string().min(1, "Name is required").max(64),
  agent_id: z.string().optional(),
  secret: z.string().optional(),
});

export type CreateAgentInput = z.infer<typeof createAgentSchema>;

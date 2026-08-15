import type { APIUpstreamAuthType, APIUpstreamCredential } from "@/lib/api/api-services";

export function credentialFor(authType: APIUpstreamAuthType, fields: Record<string, string>): APIUpstreamCredential | undefined {
  if (authType === "bearer" && fields.bearer_token) return { bearer_token: fields.bearer_token };
  if (authType === "header" && fields.header_name && fields.header_value) return { header_name: fields.header_name, header_value: fields.header_value };
  if (authType === "query" && fields.query_name && fields.query_value) return { query_name: fields.query_name, query_value: fields.query_value };
  if (authType === "basic" && fields.basic_username && fields.basic_password) return { basic_username: fields.basic_username, basic_password: fields.basic_password };
  return undefined;
}

export function credentialComplete(authType: APIUpstreamAuthType, credential: APIUpstreamCredential | undefined) {
  const current = credential ?? {};
  return authType === "none" || Boolean(
    authType === "bearer" ? current.bearer_token
      : authType === "header" ? current.header_name && current.header_value
        : authType === "query" ? current.query_name && current.query_value
          : current.basic_username && current.basic_password,
  );
}

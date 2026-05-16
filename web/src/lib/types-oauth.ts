export interface PublicProvider {
  name: string;
  display_name: string;
  icon_url?: string;
}

export interface OAuthProvider {
  id: number;
  name: string;
  display_name: string;
  issuer?: string;
  authorization_endpoint: string;
  token_endpoint: string;
  userinfo_endpoint: string;
  jwks_uri?: string;
  client_id: string;
  client_secret?: string;
  scopes?: string;
  icon_url?: string;
  enabled: boolean;
  protocol?: "oidc" | "feishu";
  created_at: number;
  updated_at: number;
}

export interface OAuthIdentityItem {
  id: number;
  provider_id: number;
  provider_name: string;
  provider_display_name: string;
  subject: string;
  email?: string;
  display_name?: string;
  created_at: number;
}

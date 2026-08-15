import { useState } from "react";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { UpstreamCredentialFields } from "./upstream-credential-fields";
import type { APIUpstreamAuthType, APIUpstreamCredential } from "@/lib/api/api-services";

vi.mock("next-intl", () => ({ useTranslations: () => (key: string) => ({ bearerToken: "Bearer token", headerName: "Header name", headerValue: "Header value", queryName: "Query parameter name", queryValue: "Query parameter value", basicUsername: "Basic username", basicPassword: "Basic password" })[key] ?? key }));

function CredentialHarness({ authType }: { authType: APIUpstreamAuthType }) {
  const [credential, setCredential] = useState<APIUpstreamCredential>({});
  return <><UpstreamCredentialFields idPrefix="inline" authType={authType} credential={credential} onChange={setCredential} /><output role="status">{JSON.stringify(credential)}</output></>;
}

describe("UpstreamCredentialFields", () => {
  it("edits a bearer secret through one token field", async () => {
    const user = userEvent.setup();
    render(<CredentialHarness authType="bearer" />);
    expect(screen.queryByLabelText("Header name")).not.toBeInTheDocument();
    await user.type(screen.getByLabelText("Bearer token"), "secret");
    expect(screen.getByRole("status")).toHaveTextContent('{"bearer_token":"secret"}');
  });

  it("keeps query name and value in their correct credential keys", async () => {
    const user = userEvent.setup();
    render(<CredentialHarness authType="query" />);
    await user.type(screen.getByLabelText("Query parameter name"), "api_key");
    await user.type(screen.getByLabelText("Query parameter value"), "secret");
    expect(screen.getByRole("status")).toHaveTextContent('{"query_name":"api_key","query_value":"secret"}');
  });

  it("keeps basic username/password and header name/value distinct", async () => {
    const user = userEvent.setup();
    const { rerender } = render(<CredentialHarness authType="basic" />);
    await user.type(screen.getByLabelText("Basic username"), "operator");
    await user.type(screen.getByLabelText("Basic password"), "password");
    expect(screen.getByRole("status")).toHaveTextContent('{"basic_username":"operator","basic_password":"password"}');
    rerender(<CredentialHarness key="header" authType="header" />);
    await user.type(screen.getByLabelText("Header name"), "X-Key");
    await user.type(screen.getByLabelText("Header value"), "secret");
    expect(screen.getByRole("status")).toHaveTextContent('{"header_name":"X-Key","header_value":"secret"}');
  });

  it("renders no secret input for none authentication", () => {
    render(<CredentialHarness authType="none" />);
    expect(screen.queryByRole("textbox")).not.toBeInTheDocument();
  });
});

import { render, screen } from "@testing-library/react";
import { NextIntlClientProvider } from "next-intl";
import { beforeEach, describe, expect, it, vi } from "vitest";

import en from "@/i18n/en.json";
import zh from "@/i18n/zh.json";

import { EntityMultiPicker } from "./entity-multi-picker";

const { useEntityOptions } = vi.hoisted(() => ({ useEntityOptions: vi.fn() }));

vi.mock("./use-entity-options", () => ({ useEntityOptions }));
vi.mock("./registry", () => ({
  ENTITY_ADAPTERS: { "api-access-token": {} },
}));

describe("EntityMultiPicker runtime translations", () => {
  beforeEach(() => {
    useEntityOptions.mockReturnValue({
      search: "",
      setSearch: vi.fn(),
      items: [],
      isLoading: false,
      isError: false,
      refetch: vi.fn(),
      getValue: vi.fn(),
      renderItem: vi.fn(),
    });
  });

  it.each([
    ["en", en, "Select API access token"],
    ["zh", zh, "选择 API 调用 Token"],
  ] as const)("renders the API access Token placeholder in %s", (locale, messages, placeholder) => {
    render(
      <NextIntlClientProvider locale={locale} messages={messages}>
        <EntityMultiPicker entity="api-access-token" value={[]} onChange={vi.fn()} />
      </NextIntlClientProvider>,
    );

    expect(screen.getByText(placeholder).closest('[role="combobox"]')).toBeInTheDocument();
  });
});

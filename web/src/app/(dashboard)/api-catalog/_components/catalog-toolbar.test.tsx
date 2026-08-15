import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import { CatalogTokenPicker } from "./catalog-toolbar";

const picker = vi.hoisted(() => ({ props: vi.fn() }));

vi.mock("next-intl", () => ({ useTranslations: () => (key: string) => key }));
vi.mock("@/components/business/entity-picker/entity-picker", () => ({
  EntityPicker: (props: { defaultAdminScope?: string; onChange: (value: string) => void }) => {
    picker.props(props);
    return <>
      <button type="button" onClick={() => props.onChange("17")}>choose</button>
      <button type="button" onClick={() => props.onChange("")}>clear</button>
    </>;
  },
}));

describe("CatalogTokenPicker", () => {
  it("uses the independent all-user Token scope and forwards choose and clear", () => {
    const onTokenChange = vi.fn();
    const onTokenClear = vi.fn();
    render(<CatalogTokenPicker id="catalog-token-picker" label="Token" tokenID={0} onTokenChange={onTokenChange} onTokenClear={onTokenClear} />);

    expect(picker.props).toHaveBeenLastCalledWith(expect.objectContaining({
      entity: "usable-token",
      defaultAdminScope: "all",
      value: "",
    }));
    fireEvent.click(screen.getByRole("button", { name: "choose" }));
    fireEvent.click(screen.getByRole("button", { name: "clear" }));
    expect(onTokenChange).toHaveBeenCalledWith(17);
    expect(onTokenClear).toHaveBeenCalledOnce();
  });
});

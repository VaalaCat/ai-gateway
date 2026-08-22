import json
import shutil
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path


SOURCE_SCRIPT = Path(__file__).with_name("verify-i18n.py")


class VerifyI18NTest(unittest.TestCase):
    def setUp(self) -> None:
        self.temporary_directory = tempfile.TemporaryDirectory()
        self.root = Path(self.temporary_directory.name)
        (self.root / "scripts").mkdir()
        (self.root / "web" / "src" / "i18n").mkdir(parents=True)
        shutil.copy2(SOURCE_SCRIPT, self.root / "scripts" / "verify-i18n.py")

    def tearDown(self) -> None:
        self.temporary_directory.cleanup()

    def write_tsx(self, relative_path: str, source: str) -> None:
        path = self.root / "web" / "src" / relative_path
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(source, encoding="utf-8")

    def write_locales(self, zh: dict, en: dict) -> None:
        locale_dir = self.root / "web" / "src" / "i18n"
        (locale_dir / "zh.json").write_text(json.dumps(zh), encoding="utf-8")
        (locale_dir / "en.json").write_text(json.dumps(en), encoding="utf-8")

    def run_verifier(self) -> subprocess.CompletedProcess[str]:
        return subprocess.run(
            [sys.executable, str(self.root / "scripts" / "verify-i18n.py")],
            cwd=self.root,
            check=False,
            capture_output=True,
            text=True,
        )

    def test_accepts_namespace_translator_passed_to_child(self) -> None:
        self.write_tsx(
            "parent.tsx",
            '''
import { useTranslations } from "next-intl";
import { Child } from "./child";
export function Parent() {
  const t = useTranslations("apiServices");
  return <Child t={t} />;
}
''',
        )
        self.write_tsx(
            "child.tsx",
            '''
export function Child({ t }: { t: (key: string) => string }) {
  return <span>{t("childTitle")}</span>;
}
''',
        )
        self.write_locales(
            {"apiServices": {"childTitle": "子项"}},
            {"apiServices": {"childTitle": "Child"}},
        )

        result = self.run_verifier()

        self.assertEqual(0, result.returncode, result.stderr)
        self.assertEqual("i18n verify OK\n", result.stdout)

    def test_rejects_key_that_exists_only_in_the_wrong_namespace(self) -> None:
        self.write_tsx(
            "parent.tsx",
            '''
import { useTranslations } from "next-intl";
import { Child } from "./child";
export function Parent() {
  const t = useTranslations("apiServices");
  return <Child t={t} />;
}
''',
        )
        self.write_tsx(
            "child.tsx",
            '''
export function Child({ t }: { t: (key: string) => string }) {
  return <span>{t("wrongOnly")}</span>;
}
''',
        )
        self.write_locales(
            {"other": {"wrongOnly": "错误位置"}},
            {"other": {"wrongOnly": "Wrong namespace"}},
        )

        result = self.run_verifier()

        self.assertEqual(1, result.returncode)
        self.assertIn("missing key 'wrongOnly' in zh", result.stderr)
        self.assertIn("missing key 'wrongOnly' in en", result.stderr)

    def test_propagates_namespace_through_forwarding_component(self) -> None:
        self.write_tsx(
            "parent.tsx",
            '''
import { useTranslations } from "next-intl";
import { Wrapper } from "./wrapper";
export function Parent() {
  const t = useTranslations("apiServices");
  return <Wrapper t={t} />;
}
''',
        )
        self.write_tsx(
            "wrapper.tsx",
            '''
import { Child } from "./nested/child";
export function Wrapper({ t }: { t: (key: string) => string }) {
  return <Child t={t} />;
}
''',
        )
        self.write_tsx(
            "nested/child.tsx",
            '''
export function Child({ t }: { t: (key: string) => string }) {
  return <span>{t("nestedTitle")}</span>;
}
''',
        )
        self.write_locales(
            {"apiServices": {"nestedTitle": "嵌套项"}},
            {"apiServices": {"nestedTitle": "Nested child"}},
        )

        result = self.run_verifier()

        self.assertEqual(0, result.returncode, result.stderr)

    def test_accepts_actual_translator_identifier_and_multiple_translators(self) -> None:
        self.write_tsx(
            "page.tsx",
            '''
import { useTranslations } from "next-intl";
export function Page() {
  const translate = useTranslations("page");
  const formatCommon = useTranslations("common");
  return <>{translate("title")}{formatCommon("save")}</>;
}
''',
        )
        self.write_locales(
            {"page": {"title": "标题"}, "common": {"save": "保存"}},
            {"page": {"title": "Title"}, "common": {"save": "Save"}},
        )

        result = self.run_verifier()

        self.assertEqual(0, result.returncode, result.stderr)

    def test_rejects_missing_key_for_aliased_translator(self) -> None:
        self.write_tsx(
            "page.tsx",
            '''
import { useTranslations } from "next-intl";
export function Page() {
  const translate = useTranslations("page");
  return <>{translate("missing")}</>;
}
''',
        )
        self.write_locales({"page": {}}, {"page": {}})

        result = self.run_verifier()

        self.assertEqual(1, result.returncode)
        self.assertIn("missing key 'missing' in zh", result.stderr)
        self.assertIn("missing key 'missing' in en", result.stderr)

    def test_rejects_bound_dotted_key_from_wrong_location(self) -> None:
        self.write_tsx(
            "page.tsx",
            '''
import { useTranslations } from "next-intl";
export function Page() {
  const translate = useTranslations("page");
  return <>{translate("wrong.key")}</>;
}
''',
        )
        self.write_locales(
            {"wrong": {"key": "错误位置"}},
            {"wrong": {"key": "Wrong location"}},
        )

        result = self.run_verifier()

        self.assertEqual(1, result.returncode)

    def test_accepts_fully_qualified_key_without_namespace(self) -> None:
        self.write_tsx(
            "leaf.tsx",
            '''
export function Leaf({ t }: { t: (key: string) => string }) {
  return <>{t("common.save")}</>;
}
''',
        )
        self.write_locales(
            {"common": {"save": "保存"}},
            {"common": {"save": "Save"}},
        )

        result = self.run_verifier()

        self.assertEqual(0, result.returncode, result.stderr)

    def test_single_namespace_does_not_scope_explicit_unscoped_callback(self) -> None:
        self.write_tsx(
            "page.tsx",
            '''
import { useTranslations } from "next-intl";
const renderAction = (t: (key: string) => string) => t("common.save");
export function Page() {
  const translate = useTranslations("page");
  return <>{translate("title")}{renderAction((key) => key)}</>;
}
''',
        )
        self.write_locales(
            {"page": {"title": "标题", "common": {"save": "错误位置"}}},
            {"page": {"title": "Title", "common": {"save": "Wrong location"}}},
        )

        result = self.run_verifier()

        self.assertEqual(1, result.returncode)
        self.assertIn("missing key 'common.save' in zh", result.stderr)
        self.assertIn("missing key 'common.save' in en", result.stderr)

    def test_unresolved_legacy_translator_fails_closed(self) -> None:
        self.write_tsx(
            "page.tsx",
            '''
export function Page() {
  return <>{t("common.save")}</>;
}
''',
        )
        self.write_locales(
            {"common": {"save": "保存"}},
            {"common": {"save": "Save"}},
        )

        result = self.run_verifier()

        self.assertEqual(1, result.returncode)
        self.assertIn("unresolved translator t", result.stderr)

    def test_distinguishes_translator_callbacks_by_return_type(self) -> None:
        cases = {
            "boolean callback is not a translator": (
                '''
export function Page({ has }: { has: (key: string) => boolean }) {
  return <>{String(has("ordinary.boolean.key"))}</>;
}
''',
                {},
                0,
            ),
            "number callback is not a translator": (
                '''
export function Page({ compare }: { compare: (key: string) => number }) {
  return <>{compare("ordinary.number.key")}</>;
}
''',
                {},
                0,
            ),
            "string translator key exists": (
                '''
export function Page({ translate }: { translate: (key: string, values?: Record<string, unknown>) => string }) {
  return <>{translate("title")}</>;
}
''',
                {"title": "Title"},
                0,
            ),
            "string translator key is missing": (
                '''
export function Page({ translate }: { translate: (key: string, values?: Record<string, unknown>) => string }) {
  return <>{translate("missing")}</>;
}
''',
                {},
                1,
            ),
        }
        for name, (source, locale, expected_returncode) in cases.items():
            with self.subTest(name=name):
                self.write_tsx("page.tsx", source)
                self.write_locales(locale, locale)

                result = self.run_verifier()

                self.assertEqual(expected_returncode, result.returncode, result.stderr)
                if expected_returncode:
                    self.assertIn("missing key 'missing' in zh", result.stderr)
                    self.assertIn("missing key 'missing' in en", result.stderr)

    def test_accepts_explicit_scoped_callback_binding(self) -> None:
        self.write_tsx(
            "page.tsx",
            '''
import { useTranslations } from "next-intl";
const renderAction = (t: ReturnType<typeof useTranslations<"page">>) => t("save");
export function Page() {
  const translate = useTranslations("page");
  return <>{translate("title")}{renderAction(translate)}</>;
}
''',
        )
        self.write_locales(
            {"page": {"title": "标题", "save": "保存"}},
            {"page": {"title": "Title", "save": "Save"}},
        )

        result = self.run_verifier()

        self.assertEqual(0, result.returncode, result.stderr)

    def test_supports_component_export_syntax_with_scoped_translator(self) -> None:
        cases = {
            "default-arrow": (
                "import Child from './child';",
                'export default ({ t }: { t: (key: string) => string }) => <>{t("title")}</>;',
                "<Child t={translate} />",
            ),
            "anonymous-default-function": (
                "import Child from './child';",
                'export default function ({ t }: { t: (key: string) => string }) { return <>{t("title")}</>; }',
                "<Child t={translate} />",
            ),
            "exported-named-arrow": (
                "import { Child } from './child';",
                'export const Child = ({ t }: { t: (key: string) => string }) => <>{t("title")}</>;',
                "<Child t={translate} />",
            ),
            "default-exported-local-arrow": (
                "import Child from './child';",
                'const Child = ({ t }: { t: (key: string) => string }) => <>{t("title")}</>; export default Child;',
                "<Child t={translate} />",
            ),
        }
        for name, (child_import, child_source, child_usage) in cases.items():
            with self.subTest(name=name, locale="correct"):
                self.write_tsx(
                    "parent.tsx",
                    f'''
import {{ useTranslations }} from "next-intl";
{child_import}
export function Parent() {{
  const translate = useTranslations("page");
  return {child_usage};
}}
''',
                )
                self.write_tsx("child.tsx", child_source)
                self.write_locales(
                    {"page": {"title": "标题"}},
                    {"page": {"title": "Title"}},
                )

                result = self.run_verifier()

                self.assertEqual(0, result.returncode, result.stderr)

            with self.subTest(name=name, locale="missing"):
                self.write_locales({"page": {}}, {"page": {}})

                result = self.run_verifier()

                self.assertEqual(1, result.returncode)
                self.assertIn("missing key 'title' in zh", result.stderr)
                self.assertIn("missing key 'title' in en", result.stderr)

    def test_supports_default_import_and_prop_rename(self) -> None:
        self.write_tsx(
            "parent.tsx",
            '''
import { useTranslations } from "next-intl";
import Child from "./child";
export function Parent() {
  const translate = useTranslations("page");
  return <Child translator={translate} />;
}
''',
        )
        self.write_tsx(
            "child.tsx",
            '''
export default function Child({ translator: childTranslate }: { translator: (key: string) => string }) {
  return <>{childTranslate("title")}</>;
}
''',
        )
        self.write_locales(
            {"page": {"title": "标题"}},
            {"page": {"title": "Title"}},
        )

        result = self.run_verifier()

        self.assertEqual(0, result.returncode, result.stderr)

    def test_supports_named_alias_and_default_as_imports(self) -> None:
        self.write_tsx(
            "parent.tsx",
            '''
import { useTranslations } from "next-intl";
import { Child as RenamedChild } from "./named-child";
import { default as DefaultChild } from "./default-child";
export function Parent() {
  const translate = useTranslations("page");
  return <><RenamedChild translator={translate} /><DefaultChild t={translate} /></>;
}
''',
        )
        self.write_tsx(
            "named-child.tsx",
            '''
export function Child({ translator }: { translator: (key: string) => string }) {
  return <>{translator("named")}</>;
}
''',
        )
        self.write_tsx(
            "default-child.tsx",
            '''
export default function Child({ t }: { t: (key: string) => string }) {
  return <>{t("default")}</>;
}
''',
        )
        self.write_locales(
            {"page": {"named": "命名", "default": "默认"}},
            {"page": {"named": "Named", "default": "Default"}},
        )

        result = self.run_verifier()

        self.assertEqual(0, result.returncode, result.stderr)

    def test_rejects_child_key_missing_from_one_reachable_namespace(self) -> None:
        self.write_tsx(
            "parent-a.tsx",
            '''
import { useTranslations } from "next-intl";
import { Child } from "./child";
export function ParentA() {
  const t = useTranslations("a");
  return <Child t={t} />;
}
''',
        )
        self.write_tsx(
            "parent-b.tsx",
            '''
import { useTranslations } from "next-intl";
import { Child } from "./child";
export function ParentB() {
  const t = useTranslations("b");
  return <Child t={t} />;
}
''',
        )
        self.write_tsx(
            "child.tsx",
            '''
export function Child({ t }: { t: (key: string) => string }) {
  return <>{t("shared")}</>;
}
''',
        )
        self.write_locales(
            {"a": {"shared": "仅 A"}, "b": {}},
            {"a": {"shared": "A only"}, "b": {}},
        )

        result = self.run_verifier()

        self.assertEqual(1, result.returncode)
        self.assertIn("missing key 'shared' in zh", result.stderr)
        self.assertIn("missing key 'shared' in en", result.stderr)

    def test_component_identity_prevents_namespace_leak_between_exports(self) -> None:
        self.write_tsx(
            "parent.tsx",
            '''
import { useTranslations } from "next-intl";
import { First, Second } from "./children";
export function Parent() {
  const first = useTranslations("a");
  const second = useTranslations("b");
  return <><First t={first} /><Second t={second} /></>;
}
''',
        )
        self.write_tsx(
            "children.tsx",
            '''
export function First({ t }: { t: (key: string) => string }) { return <>{t("first")}</>; }
export function Second({ t }: { t: (key: string) => string }) { return <>{t("second")}</>; }
''',
        )
        self.write_locales(
            {"a": {"first": "一"}, "b": {"second": "二"}},
            {"a": {"first": "One"}, "b": {"second": "Two"}},
        )

        result = self.run_verifier()

        self.assertEqual(0, result.returncode, result.stderr)

    def test_translator_propagation_cycle_terminates(self) -> None:
        self.write_tsx(
            "parent.tsx",
            '''
import { useTranslations } from "next-intl";
import { A } from "./a";
export function Parent() {
  const t = useTranslations("cycle");
  return <A t={t} />;
}
''',
        )
        self.write_tsx(
            "a.tsx",
            '''
import { B } from "./b";
export function A({ t }: { t: (key: string) => string }) { return <B t={t} />; }
''',
        )
        self.write_tsx(
            "b.tsx",
            '''
import { A } from "./a";
export function B({ t }: { t: (key: string) => string }) { return <><span>{t("done")}</span><A t={t} /></>; }
''',
        )
        self.write_locales(
            {"cycle": {"done": "完成"}},
            {"cycle": {"done": "Done"}},
        )

        result = self.run_verifier()

        self.assertEqual(0, result.returncode, result.stderr)


if __name__ == "__main__":
    unittest.main()

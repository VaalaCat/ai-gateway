#!/usr/bin/env python3
"""Verify every t("key") call referenced in .tsx has a translation in zh.json AND en.json.

Detects:
- `const t = useTranslations("ns")` binds one translation function to its namespace
- `<Child t={t} />` propagates that namespace through relative component imports
- `t("key.subkey")` (and `tc(...)` / `tf(...)`) is checked against the bound namespace

A .tsx file may bind zero or more translation functions. Explicitly unscoped
translator parameters check top-level keys; unresolved legacy t/tc/tf calls fail closed.

Exit non-zero if any key is missing in zh.json or en.json (with a list).
"""
import json
import re
import sys
from dataclasses import dataclass
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
SRC = ROOT / "web" / "src"
LOCALE_DIR = ROOT / "web" / "src" / "i18n"
LOCALE_FILES = {
    "zh": LOCALE_DIR / "zh.json",
    "en": LOCALE_DIR / "en.json",
}

USE_TR_BINDING_RE = re.compile(
    r'\b(?:const|let|var)\s+([A-Za-z_$][\w$]*)\s*=\s*useTranslations\(\s*"([^"]+)"\s*\)'
)
UNSCOPED_TR_BINDING_RE = re.compile(
    r'\b(?:const|let|var)\s+([A-Za-z_$][\w$]*)\s*=\s*useTranslations\(\s*\)'
)
TYPED_TR_BINDING_RE = re.compile(
    r'\b([A-Za-z_$][\w$]*)\s*:\s*ReturnType<\s*typeof\s+useTranslations<\s*"([^"]+)"\s*>\s*>'
)
TRANSLATION_CALL_RE = re.compile(
    r'(?<![.\w$])([A-Za-z_$][\w$]*)\(\s*"([^"\\]*(?:\\.[^"\\]*)*)"'
)
IMPORT_RE = re.compile(
    r'\bimport\s+([\s\S]*?)\s+from\s*["\']([^"\']+)["\']\s*;', re.DOTALL
)
FUNCTION_RE = re.compile(
    r'(?:(export)\s+)?(?:(default)\s+)?function(?:\s+([A-Za-z_$][\w$]*))?\s*(?:<[^>{}]*>)?\s*\(',
    re.DOTALL,
)
NAMED_ARROW_RE = re.compile(
    r'(?:(export)\s+)?(?:const|let|var)\s+([A-Za-z_$][\w$]*)\s*=\s*(?:async\s*)?\('
)
DEFAULT_ARROW_RE = re.compile(r'\bexport\s+default\s+(?:async\s*)?\(')
DEFAULT_EXPORT_IDENTIFIER_RE = re.compile(
    r'\bexport\s+default\s+([A-Za-z_$][\w$]*)\s*;'
)
SIMPLE_ARROW_RE = re.compile(
    r'\(\s*([A-Za-z_$][\w$]*(?:\s*,\s*[A-Za-z_$][\w$]*)*)\s*\)\s*=>'
)
TRANSLATOR_FUNCTION_TYPE_RE = re.compile(
    r'\b([A-Za-z_$][\w$]*)\s*:\s*\(\s*[A-Za-z_$][\w$]*\s*:\s*string\b[^)]*\)\s*=>\s*string\b'
)
TRANSLATOR_TYPE_ALIAS_RE = re.compile(
    r'\btype\s+([A-Za-z_$][\w$]*)\s*=\s*\(\s*[A-Za-z_$][\w$]*\s*:\s*string\b[^)]*\)\s*=>\s*string\b'
)
SCOPED_TRANSLATOR_TYPE_ALIAS_RE = re.compile(
    r'\btype\s+([A-Za-z_$][\w$]*)\s*=\s*ReturnType<\s*typeof\s+useTranslations<\s*"([^"]+)"\s*>\s*>\s*;'
)
ALIAS_TYPED_PARAMETER_RE = re.compile(
    r'\b([A-Za-z_$][\w$]*)\s*:\s*([A-Za-z_$][\w$]*)\b'
)
JSX_IDENTIFIER_PROP_RE = re.compile(
    r'\b([A-Za-z_$][\w$]*)\s*=\s*\{\s*([A-Za-z_$][\w$]*)\s*\}'
)
TRANSLATOR_ALIAS_RE = re.compile(
    r'\bconst\s+([A-Za-z_$][\w$]*)(?:\s*:[^=;]+)?\s*=\s*'
    r'(?:useCallback\(\s*)?\([^)]*\)\s*=>\s*([A-Za-z_$][\w$]*)\s*\('
)

MODULE_COMPONENT = "<module>"
LEGACY_UNBOUND_TRANSLATORS = {"t", "tc", "tf"}


@dataclass(frozen=True)
class ComponentID:
    path: Path
    name: str


@dataclass(frozen=True)
class Component:
    identity: ComponentID
    start: int
    end: int
    props: dict[str, str]
    parameters: tuple[str, ...]
    explicit_translators: tuple[str, ...]
    translator_types: tuple[tuple[str, str], ...]


@dataclass(frozen=True)
class ImportedComponent:
    path: Path
    export_name: str


@dataclass(frozen=True)
class TranslatorEdge:
    parent: ComponentID
    source_name: str
    target: ImportedComponent | ComponentID
    target_prop: str


@dataclass
class ComponentIndex:
    components: dict[Path, list[Component]]
    exports: dict[Path, dict[str, ComponentID]]
    imports: dict[Path, dict[str, ImportedComponent]]
    locals: dict[Path, dict[str, ComponentID]]
    by_id: dict[ComponentID, Component]


def load_locale(path: Path) -> dict:
    return json.loads(path.read_text(encoding="utf-8"))


def flatten_keys(d: dict, prefix: str = "") -> set[str]:
    out: set[str] = set()
    for k, v in d.items():
        key = f"{prefix}.{k}" if prefix else k
        if isinstance(v, dict):
            out.update(flatten_keys(v, key))
        else:
            out.add(key)
    return out


def find_closing(text: str, opening: int, open_char: str, close_char: str) -> int | None:
    """Find a matching delimiter while ignoring comments and quoted/template strings."""
    depth = 0
    quote: str | None = None
    escaped = False
    line_comment = False
    block_comment = False
    index = opening
    while index < len(text):
        char = text[index]
        following = text[index + 1] if index + 1 < len(text) else ""
        if line_comment:
            if char == "\n":
                line_comment = False
        elif block_comment:
            if char == "*" and following == "/":
                block_comment = False
                index += 1
        elif quote:
            if escaped:
                escaped = False
            elif char == "\\":
                escaped = True
            elif char == quote:
                quote = None
        elif char == "/" and following == "/":
            line_comment = True
            index += 1
        elif char == "/" and following == "*":
            block_comment = True
            index += 1
        elif char in {'"', "'", "`"}:
            quote = char
        elif char == open_char:
            depth += 1
        elif char == close_char:
            depth -= 1
            if depth == 0:
                return index
        index += 1
    return None


def destructured_props(parameters: str) -> dict[str, str]:
    opening = parameters.find("{")
    if opening < 0:
        return {}
    closing = find_closing(parameters, opening, "{", "}")
    if closing is None:
        return {}
    props: dict[str, str] = {}
    for entry in parameters[opening + 1 : closing].split(","):
        declaration = entry.strip().split("=", 1)[0].strip()
        match = re.fullmatch(
            r'([A-Za-z_$][\w$]*)(?:\s*:\s*([A-Za-z_$][\w$]*))?', declaration
        )
        if match:
            props[match.group(1)] = match.group(2) or match.group(1)
    return props


def split_top_level(text: str) -> list[str]:
    parts: list[str] = []
    start = 0
    round_depth = square_depth = brace_depth = angle_depth = 0
    quote: str | None = None
    escaped = False
    for index, char in enumerate(text):
        if quote:
            if escaped:
                escaped = False
            elif char == "\\":
                escaped = True
            elif char == quote:
                quote = None
            continue
        if char in {'"', "'", "`"}:
            quote = char
        elif char == "(":
            round_depth += 1
        elif char == ")":
            round_depth = max(0, round_depth - 1)
        elif char == "[":
            square_depth += 1
        elif char == "]":
            square_depth = max(0, square_depth - 1)
        elif char == "{":
            brace_depth += 1
        elif char == "}":
            brace_depth = max(0, brace_depth - 1)
        elif char == "<":
            angle_depth += 1
        elif char == ">":
            angle_depth = max(0, angle_depth - 1)
        elif char == "," and not (round_depth or square_depth or brace_depth or angle_depth):
            parts.append(text[start:index])
            start = index + 1
    parts.append(text[start:])
    return parts


def positional_parameters(parameters: str) -> tuple[str, ...]:
    names: list[str] = []
    for parameter in split_top_level(parameters):
        match = re.match(r"\s*([A-Za-z_$][\w$]*)\b", parameter)
        names.append(match.group(1) if match else "")
    return tuple(names)


def translator_type_aliases(text: str) -> tuple[set[str], dict[str, str]]:
    unscoped = set(TRANSLATOR_TYPE_ALIAS_RE.findall(text))
    scoped = dict(SCOPED_TRANSLATOR_TYPE_ALIAS_RE.findall(text))
    return unscoped, scoped


def translator_prop_containers(
    text: str, translator_aliases: set[str]
) -> dict[str, dict[str, str]]:
    containers: dict[str, dict[str, str]] = {}
    declaration_re = re.compile(
        r"\b(?:interface|type)\s+([A-Za-z_$][\w$]*)[^={;]*(?:=\s*)?\{"
    )
    for declaration in declaration_re.finditer(text):
        opening = declaration.end() - 1
        closing = find_closing(text, opening, "{", "}")
        if closing is None:
            continue
        members = {
            match.group(1): match.group(2)
            for match in ALIAS_TYPED_PARAMETER_RE.finditer(text[opening + 1 : closing])
            if match.group(2) in translator_aliases
        }
        if members:
            containers[declaration.group(1)] = members
    return containers


def explicit_translator_parameters(
    parameters: str,
    props: dict[str, str],
    translator_aliases: set[str],
    prop_containers: dict[str, dict[str, str]],
) -> tuple[tuple[str, ...], tuple[tuple[str, str], ...]]:
    names: list[str] = []
    typed_names: list[tuple[str, str]] = []
    for match in TRANSLATOR_FUNCTION_TYPE_RE.finditer(parameters):
        local_name = props.get(match.group(1), match.group(1))
        if local_name not in names:
            names.append(local_name)
    for match in ALIAS_TYPED_PARAMETER_RE.finditer(parameters):
        if match.group(2) not in translator_aliases:
            continue
        local_name = props.get(match.group(1), match.group(1))
        if local_name not in names:
            names.append(local_name)
        pair = (local_name, match.group(2))
        if pair not in typed_names:
            typed_names.append(pair)
    container_match = re.search(r"}\s*:\s*([A-Za-z_$][\w$]*)\b", parameters)
    if container_match is not None:
        for prop_name, alias in prop_containers.get(container_match.group(1), {}).items():
            local_name = props.get(prop_name)
            if local_name is None:
                continue
            if local_name not in names:
                names.append(local_name)
            pair = (local_name, alias)
            if pair not in typed_names:
                typed_names.append(pair)
    return tuple(names), tuple(typed_names)


def expression_end(text: str, start: int) -> int:
    round_depth = square_depth = brace_depth = 0
    quote: str | None = None
    escaped = False
    for index in range(start, len(text)):
        char = text[index]
        if quote:
            if escaped:
                escaped = False
            elif char == "\\":
                escaped = True
            elif char == quote:
                quote = None
            continue
        if char in {'"', "'", "`"}:
            quote = char
        elif char == "(":
            round_depth += 1
        elif char == ")":
            round_depth = max(0, round_depth - 1)
        elif char == "[":
            square_depth += 1
        elif char == "]":
            square_depth = max(0, square_depth - 1)
        elif char == "{":
            brace_depth += 1
        elif char == "}":
            brace_depth = max(0, brace_depth - 1)
        elif char == ";" and not (round_depth or square_depth or brace_depth):
            return index + 1
    return len(text)


def make_component(
    path: Path,
    text: str,
    identity: ComponentID,
    start: int,
    parameter_open: int,
    function_body: bool,
    translator_aliases: set[str],
    prop_containers: dict[str, dict[str, str]],
) -> Component | None:
    parameter_close = find_closing(text, parameter_open, "(", ")")
    if parameter_close is None:
        return None
    body_start = parameter_close + 1
    if function_body:
        body_start = text.find("{", body_start)
        if body_start < 0:
            return None
    else:
        arrow = re.match(r"\s*(?::\s*[^=;\n]+)?=>", text[body_start:])
        if arrow is None:
            return None
        body_start += arrow.end()
    while body_start < len(text) and text[body_start].isspace():
        body_start += 1
    if body_start < len(text) and text[body_start] == "{":
        body_close = find_closing(text, body_start, "{", "}")
        if body_close is None:
            return None
        end = body_close + 1
    else:
        end = expression_end(text, body_start)
    parameters = text[parameter_open + 1 : parameter_close]
    props = destructured_props(parameters)
    explicit_translators, translator_types = explicit_translator_parameters(
        parameters, props, translator_aliases, prop_containers
    )
    return Component(
        identity,
        start,
        end,
        props,
        positional_parameters(parameters),
        explicit_translators,
        translator_types,
    )


def components_in(path: Path, text: str) -> tuple[list[Component], dict[str, ComponentID]]:
    components: list[Component] = []
    exports: dict[str, ComponentID] = {}
    unscoped_aliases, scoped_aliases = translator_type_aliases(text)
    translator_aliases = unscoped_aliases | set(scoped_aliases)
    prop_containers = translator_prop_containers(text, translator_aliases)
    claimed_parameter_openings: set[int] = set()
    for match in FUNCTION_RE.finditer(text):
        name = match.group(3)
        if name is None and not (match.group(1) and match.group(2)):
            continue
        identity = ComponentID(path, name or "<default>")
        component = make_component(
            path, text, identity, match.start(), match.end() - 1, True,
            translator_aliases, prop_containers,
        )
        if component is None:
            continue
        components.append(component)
        claimed_parameter_openings.add(match.end() - 1)
        if match.group(1):
            exports["default" if match.group(2) else identity.name] = identity
    for match in NAMED_ARROW_RE.finditer(text):
        identity = ComponentID(path, match.group(2))
        component = make_component(
            path, text, identity, match.start(), match.end() - 1, False,
            translator_aliases, prop_containers,
        )
        if component is None:
            continue
        components.append(component)
        claimed_parameter_openings.add(match.end() - 1)
        if match.group(1):
            exports[identity.name] = identity
    for match in DEFAULT_ARROW_RE.finditer(text):
        identity = ComponentID(path, "<default>")
        component = make_component(
            path, text, identity, match.start(), match.end() - 1, False,
            translator_aliases, prop_containers,
        )
        if component is None:
            continue
        components.append(component)
        claimed_parameter_openings.add(match.end() - 1)
        exports["default"] = identity
    for match in SIMPLE_ARROW_RE.finditer(text):
        parameter_open = match.start()
        if parameter_open in claimed_parameter_openings:
            continue
        identity = ComponentID(path, f"<arrow@{parameter_open}>")
        component = make_component(
            path, text, identity, parameter_open, parameter_open, False,
            translator_aliases, prop_containers,
        )
        if component is not None:
            components.append(component)
    components_by_name = {component.identity.name: component.identity for component in components}
    for match in DEFAULT_EXPORT_IDENTIFIER_RE.finditer(text):
        identity = components_by_name.get(match.group(1))
        if identity is not None:
            exports["default"] = identity
    return components, exports


def containing_component(path: Path, position: int, components: list[Component]) -> ComponentID:
    containing = [item for item in components if item.start <= position < item.end]
    if not containing:
        return ComponentID(path, MODULE_COMPONENT)
    return min(containing, key=lambda item: item.end - item.start).identity


def jsx_elements(text: str) -> list[tuple[int, str, str]]:
    """Return JSX start tags, keeping `>` inside expression props inside the tag."""
    elements: list[tuple[int, str, str]] = []
    start_re = re.compile(r'<([A-Z][A-Za-z0-9_$]*)\b')
    for match in start_re.finditer(text):
        index = match.end()
        braces = 0
        quote: str | None = None
        escaped = False
        while index < len(text):
            char = text[index]
            if quote:
                if escaped:
                    escaped = False
                elif char == "\\":
                    escaped = True
                elif char == quote:
                    quote = None
            elif char in {'"', "'", "`"}:
                quote = char
            elif char == "{":
                braces += 1
            elif char == "}":
                braces = max(0, braces - 1)
            elif char == ">" and braces == 0:
                elements.append((match.start(), match.group(1), text[match.end() : index]))
                break
            index += 1
    return elements


def function_calls(text: str) -> list[tuple[int, str, list[str]]]:
    calls: list[tuple[int, str, list[str]]] = []
    for match in re.finditer(r"(?<![.\w$])([A-Za-z_$][\w$]*)\s*\(", text):
        opening = match.end() - 1
        closing = find_closing(text, opening, "(", ")")
        if closing is not None:
            calls.append((match.start(), match.group(1), split_top_level(text[opening + 1 : closing])))
    return calls


def declaration_assignment(text: str, start: int) -> int | None:
    position = start
    while True:
        position = text.find("=", position)
        if position < 0 or text[position : position + 2] != "=>":
            return position if position >= 0 else None
        position += 2


def contextual_translator_bindings(
    text: str, components: list[Component], scoped_aliases: dict[str, str]
) -> dict[tuple[ComponentID, str], set[str]]:
    bindings: dict[tuple[ComponentID, str], set[str]] = {}
    declaration_re = re.compile(r"\b(?:const|let|var)\s+[A-Za-z_$][\w$]*\s*:")
    for declaration in declaration_re.finditer(text):
        assignment = declaration_assignment(text, declaration.end())
        if assignment is None:
            continue
        initializer = assignment + 1
        while initializer < len(text) and text[initializer].isspace():
            initializer += 1
        if initializer >= len(text) or text[initializer] != "{":
            continue
        closing = find_closing(text, initializer, "{", "}")
        if closing is None:
            continue
        annotation = text[declaration.end() : assignment]
        typed_parameters = [
            (match.group(1), scoped_aliases[match.group(2)])
            for match in ALIAS_TYPED_PARAMETER_RE.finditer(annotation)
            if match.group(2) in scoped_aliases
        ]
        for component in components:
            if not (initializer < component.start < closing):
                continue
            for name, namespace in typed_parameters:
                if name in component.parameters:
                    bindings.setdefault((component.identity, name), set()).add(namespace)
    return bindings


def translation_bindings(
    path: Path, text: str, components: list[Component]
) -> dict[tuple[ComponentID, str], set[str]]:
    bindings: dict[tuple[ComponentID, str], set[str]] = {}
    for pattern in (USE_TR_BINDING_RE, TYPED_TR_BINDING_RE):
        for match in pattern.finditer(text):
            identity = containing_component(path, match.start(), components)
            bindings.setdefault((identity, match.group(1)), set()).add(match.group(2))
    for match in UNSCOPED_TR_BINDING_RE.finditer(text):
        identity = containing_component(path, match.start(), components)
        bindings.setdefault((identity, match.group(1)), set()).add("")
    _unscoped_aliases, scoped_aliases = translator_type_aliases(text)
    for match in ALIAS_TYPED_PARAMETER_RE.finditer(text):
        namespace = scoped_aliases.get(match.group(2))
        if namespace is None:
            continue
        identity = containing_component(path, match.start(), components)
        if identity.name != MODULE_COMPONENT:
            bindings.setdefault((identity, match.group(1)), set()).add(namespace)
    for key, values in contextual_translator_bindings(
        text, components, scoped_aliases
    ).items():
        bindings.setdefault(key, set()).update(values)
    return bindings


def resolve_relative_component(
    parent: Path, module_specifier: str, tsx_files: set[Path]
) -> Path | None:
    if not module_specifier.startswith("."):
        return None
    base = (parent.parent / module_specifier).resolve()
    candidates = [base.with_suffix(".tsx"), base / "index.tsx"]
    return next((candidate for candidate in candidates if candidate in tsx_files), None)


def relative_component_imports(
    parent: Path, tsx_text: str, tsx_files: set[Path]
) -> dict[str, ImportedComponent]:
    imports: dict[str, ImportedComponent] = {}
    for clause, module_specifier in IMPORT_RE.findall(tsx_text):
        target = resolve_relative_component(parent, module_specifier, tsx_files)
        if target is None:
            continue
        clause = clause.strip()
        if not clause.startswith("{"):
            default_name = clause.split(",", 1)[0].strip()
            if re.fullmatch(r"[A-Za-z_$][\w$]*", default_name):
                imports[default_name] = ImportedComponent(target, "default")
        named_match = re.search(r"\{([^}]*)\}", clause, re.DOTALL)
        if named_match is None:
            continue
        for raw_name in named_match.group(1).split(","):
            name = re.sub(r"^\s*type\s+", "", raw_name).strip()
            if not name:
                continue
            parts = re.split(r"\s+as\s+", name)
            export_name = parts[0].strip()
            local_name = parts[-1].strip()
            if re.fullmatch(r"[A-Za-z_$][\w$]*", local_name) and re.fullmatch(
                r"[A-Za-z_$][\w$]*", export_name
            ):
                imports[local_name] = ImportedComponent(target, export_name)
    return imports


def build_component_index(sources: dict[Path, str]) -> ComponentIndex:
    tsx_files = set(sources)
    component_lists: dict[Path, list[Component]] = {}
    exports: dict[Path, dict[str, ComponentID]] = {}
    for path, text in sources.items():
        component_lists[path], exports[path] = components_in(path, text)
    imports = {
        path: relative_component_imports(path, text, tsx_files)
        for path, text in sources.items()
    }
    local_components = {
        path: {component.identity.name: component.identity for component in components}
        for path, components in component_lists.items()
    }
    component_by_id = {
        component.identity: component
        for components in component_lists.values()
        for component in components
    }
    return ComponentIndex(
        component_lists, exports, imports, local_components, component_by_id
    )


def collect_translator_bindings(
    sources: dict[Path, str], index: ComponentIndex
) -> tuple[
    dict[tuple[ComponentID, str], set[str]],
    set[tuple[ComponentID, str]],
    list[tuple[ComponentID, str, str]],
    list[set[tuple[ComponentID, str]]],
]:
    namespaces: dict[tuple[ComponentID, str], set[str]] = {}
    for path, text in sources.items():
        for key, values in translation_bindings(
            path, text, index.components[path]
        ).items():
            namespaces.setdefault(key, set()).update(values)
    known_translators = set(namespaces)
    for component in index.by_id.values():
        known_translators.update(
            (component.identity, name) for name in component.explicit_translators
        )
    aliases: list[tuple[ComponentID, str, str]] = []
    type_groups: list[set[tuple[ComponentID, str]]] = []
    for path, text in sources.items():
        for match in TRANSLATOR_ALIAS_RE.finditer(text):
            aliases.append(
                (
                    containing_component(path, match.start(), index.components[path]),
                    match.group(1),
                    match.group(2),
                )
            )
        groups: dict[str, set[tuple[ComponentID, str]]] = {}
        for component in index.components[path]:
            for name, type_alias in component.translator_types:
                groups.setdefault(type_alias, set()).add((component.identity, name))
        for match in ALIAS_TYPED_PARAMETER_RE.finditer(text):
            identity = containing_component(path, match.start(), index.components[path])
            if identity.name == MODULE_COMPONENT:
                continue
            component = index.by_id[identity]
            local_name = component.props.get(match.group(1), match.group(1))
            groups.setdefault(match.group(2), set()).add((identity, local_name))
        type_groups.extend(group for group in groups.values() if group)
    return namespaces, known_translators, aliases, type_groups


def collect_translator_edges(
    sources: dict[Path, str], index: ComponentIndex
) -> list[TranslatorEdge]:
    edges: list[TranslatorEdge] = []
    for parent_path, text in sources.items():
        for jsx_start, jsx_name, jsx_attributes in jsx_elements(text):
            target: ImportedComponent | ComponentID | None = index.imports[parent_path].get(
                jsx_name
            )
            if target is None:
                target = index.locals[parent_path].get(jsx_name)
            if target is None:
                continue
            parent = containing_component(
                parent_path, jsx_start, index.components[parent_path]
            )
            for prop_name, source_name in JSX_IDENTIFIER_PROP_RE.findall(jsx_attributes):
                edges.append(TranslatorEdge(parent, source_name, target, prop_name))
        for call_start, function_name, arguments in function_calls(text):
            target = index.imports[parent_path].get(function_name)
            if target is None:
                target = index.locals[parent_path].get(function_name)
            if target is None:
                continue
            if isinstance(target, ImportedComponent):
                target_id = index.exports.get(target.path, {}).get(target.export_name)
            else:
                target_id = target
            target_component = index.by_id.get(target_id) if target_id else None
            if target_component is None:
                continue
            parent = containing_component(parent_path, call_start, index.components[parent_path])
            for position, argument in enumerate(arguments):
                source_match = re.fullmatch(r"\s*([A-Za-z_$][\w$]*)\s*", argument)
                if source_match and position < len(target_component.parameters) and target_component.parameters[position]:
                    edges.append(
                        TranslatorEdge(
                            parent,
                            source_match.group(1),
                            target,
                            f"@{target_component.parameters[position]}",
                        )
                    )
    return edges


def lexical_component_edges(index: ComponentIndex) -> list[tuple[ComponentID, ComponentID]]:
    edges: list[tuple[ComponentID, ComponentID]] = []
    for components in index.components.values():
        for child in components:
            parents = [
                parent
                for parent in components
                if parent.start < child.start and child.end <= parent.end
            ]
            if parents:
                edges.append(
                    (min(parents, key=lambda item: item.end - item.start).identity, child.identity)
                )
    return edges


def translator_edge_destination(
    edge: TranslatorEdge, index: ComponentIndex
) -> tuple[ComponentID, str] | None:
    if isinstance(edge.target, ImportedComponent):
        target_id = index.exports.get(edge.target.path, {}).get(edge.target.export_name)
    else:
        target_id = edge.target
    target_component = index.by_id.get(target_id) if target_id else None
    target_name = (
        edge.target_prop[1:]
        if edge.target_prop.startswith("@")
        else target_component.props.get(edge.target_prop) if target_component else None
    )
    if target_id is None or target_name is None:
        return None
    return target_id, target_name


def propagate_translator_context(
    index: ComponentIndex,
    namespaces: dict[tuple[ComponentID, str], set[str]],
    known_translators: set[tuple[ComponentID, str]],
    aliases: list[tuple[ComponentID, str, str]],
    type_groups: list[set[tuple[ComponentID, str]]],
    edges: list[TranslatorEdge],
) -> list[tuple[Path, str]]:
    unresolved: list[tuple[Path, str]] = []
    lexical_edges = lexical_component_edges(index)

    changed = True
    while changed:
        changed = False
        for parent, child in lexical_edges:
            child_component = index.by_id[child]
            shadowed = set(child_component.parameters) | set(child_component.props.values())
            parent_names = {
                name for identity, name in known_translators if identity == parent
            }
            for name in parent_names:
                if name in shadowed:
                    continue
                child_key = (child, name)
                if child_key not in known_translators:
                    known_translators.add(child_key)
                    changed = True
                before = len(namespaces.get(child_key, set()))
                namespaces.setdefault(child_key, set()).update(
                    namespaces.get((parent, name), set())
                )
                changed = changed or len(namespaces[child_key]) != before
        for component, alias, source in aliases:
            source_key = (component, source)
            if source_key not in known_translators:
                continue
            alias_key = (component, alias)
            if alias_key not in known_translators:
                known_translators.add(alias_key)
                changed = True
            before = len(namespaces.get(alias_key, set()))
            namespaces.setdefault(alias_key, set()).update(namespaces.get(source_key, set()))
            changed = changed or len(namespaces[alias_key]) != before
        for group in type_groups:
            if not any(item in known_translators for item in group):
                continue
            group_namespaces = set().union(
                *(namespaces.get(item, set()) for item in group)
            )
            for item in group:
                if item not in known_translators:
                    known_translators.add(item)
                    changed = True
                before = len(namespaces.get(item, set()))
                namespaces.setdefault(item, set()).update(group_namespaces)
                changed = changed or len(namespaces[item]) != before
        for edge in edges:
            source_key = (edge.parent, edge.source_name)
            if source_key not in known_translators:
                continue
            target_key = translator_edge_destination(edge, index)
            if target_key is None:
                item = (edge.parent.path, f"{edge.source_name}->{edge.target_prop}")
                if item not in unresolved:
                    unresolved.append(item)
                continue
            if target_key not in known_translators:
                known_translators.add(target_key)
                changed = True
            before = len(namespaces.get(target_key, set()))
            namespaces.setdefault(target_key, set()).update(namespaces.get(source_key, set()))
            changed = changed or len(namespaces[target_key]) != before
    return unresolved


def infer_passed_translator_namespaces(
    sources: dict[Path, str],
) -> tuple[
    dict[tuple[ComponentID, str], set[str]],
    set[tuple[ComponentID, str]],
    list[tuple[Path, str]],
    dict[Path, list[Component]],
]:
    """Follow explicit translator bindings through component and function edges."""
    index = build_component_index(sources)
    namespaces, known_translators, aliases, type_groups = collect_translator_bindings(sources, index)
    edges = collect_translator_edges(sources, index)
    unresolved = propagate_translator_context(
        index, namespaces, known_translators, aliases, type_groups, edges
    )
    return namespaces, known_translators, unresolved, index.components


def find_missing_keys(
    src: Path, locale_files: dict[str, Path]
) -> list[tuple[str, str, str]]:
    locales: dict[str, set[str]] = {
        lang: flatten_keys(load_locale(path)) for lang, path in locale_files.items()
    }
    sources = {
        path.resolve(): path.read_text(encoding="utf-8") for path in src.rglob("*.tsx")
    }
    namespaces, known_translators, unresolved, component_lists = infer_passed_translator_namespaces(sources)

    missing: list[tuple[str, str, str]] = []
    for path, edge in unresolved:
        for lang in locales:
            missing.append((str(path.relative_to(ROOT)), lang, f"unresolved translator {edge}"))
    for tsx, text in sources.items():
        for call in TRANSLATION_CALL_RE.finditer(text):
            function_name, key = call.group(1), call.group(2)
            component = containing_component(tsx, call.start(), component_lists[tsx])
            binding = (component, function_name)
            reachable_namespaces = namespaces.get(binding, set())
            if binding not in known_translators:
                if function_name in LEGACY_UNBOUND_TRANSLATORS:
                    for lang in locales:
                        missing.append(
                            (
                                str(tsx.relative_to(ROOT)),
                                lang,
                                f"unresolved translator {function_name}",
                            )
                        )
                continue
            for lang, keys in locales.items():
                if reachable_namespaces:
                    present = all(
                        (f"{namespace}.{key}" if namespace else key) in keys
                        for namespace in reachable_namespaces
                    )
                else:
                    present = key in keys
                if not present:
                    missing.append((str(tsx.relative_to(ROOT)), lang, key))
    return missing


def main() -> int:
    missing = find_missing_keys(SRC, LOCALE_FILES)

    if missing:
        # De-duplicate
        seen = set()
        for f, lang, key in missing:
            tup = (f, lang, key)
            if tup in seen:
                continue
            seen.add(tup)
            print(f"missing key {key!r} in {lang} (used in {f})", file=sys.stderr)
        return 1
    print("i18n verify OK")
    return 0


if __name__ == "__main__":
    sys.exit(main())

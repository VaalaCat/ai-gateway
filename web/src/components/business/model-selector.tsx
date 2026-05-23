"use client";

import { useState } from "react";
import { Check, ChevronsUpDown } from "lucide-react";
import { useTranslations } from "next-intl";

import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from "@/components/ui/popover";
import {
  Command,
  CommandEmpty,
  CommandGroup,
  CommandInput,
  CommandItem,
  CommandList,
} from "@/components/ui/command";
import { useModels } from "@/lib/api/models";
import { useDebounce } from "@/hooks/use-debounce";

interface ModelSelectorSingleProps {
  mode: "single";
  value: string;
  onChange: (value: string) => void;
  placeholder?: string;
}

interface ModelSelectorMultiProps {
  mode: "multi";
  value: string[];
  onChange: (value: string[]) => void;
  placeholder?: string;
}

type ModelSelectorProps = ModelSelectorSingleProps | ModelSelectorMultiProps;

export function ModelSelector(props: ModelSelectorProps) {
  const t = useTranslations("models");
  const [open, setOpen] = useState(false);
  const [search, setSearch] = useState("");
  const debouncedSearch = useDebounce(search, 300);
  const { data: modelsData } = useModels({ page_size: 100, search: debouncedSearch });

  const modelNames = (modelsData?.data ?? []).map((m) => m.model_name);

  if (props.mode === "single") {
    return (
      <Popover open={open} onOpenChange={setOpen}>
        <PopoverTrigger asChild>
          <Button variant="outline" role="combobox" className="w-full justify-between text-body">
            {props.value || props.placeholder || t("selectModel")}
            <ChevronsUpDown className="ml-2 size-4 shrink-0 opacity-50" />
          </Button>
        </PopoverTrigger>
        <PopoverContent className="w-full p-0" align="start">
          <Command>
            <CommandInput placeholder={t("searchModel")} value={search} onValueChange={setSearch} />
            <CommandList>
              <CommandEmpty>{t("noModels")}</CommandEmpty>
              <CommandGroup>
                {modelNames.map((name) => (
                  <CommandItem
                    key={name}
                    value={name}
                    onSelect={() => {
                      props.onChange(name);
                      setOpen(false);
                    }}
                  >
                    <Check className={`mr-2 size-4 ${props.value === name ? "opacity-100" : "opacity-0"}`} />
                    {name}
                  </CommandItem>
                ))}
              </CommandGroup>
            </CommandList>
          </Command>
        </PopoverContent>
      </Popover>
    );
  }

  // Multi-select mode
  const selected = new Set(props.value);
  const toggle = (name: string) => {
    const next = new Set(selected);
    if (next.has(name)) {
      next.delete(name);
    } else {
      next.add(name);
    }
    props.onChange(Array.from(next));
  };

  return (
    <div className="space-y-2">
      <Popover open={open} onOpenChange={setOpen}>
        <PopoverTrigger asChild>
          <Button variant="outline" role="combobox" className="w-full justify-between text-body">
            {selected.size > 0 ? `${selected.size} ${t("modelsSelected")}` : props.placeholder || t("selectModels")}
            <ChevronsUpDown className="ml-2 size-4 shrink-0 opacity-50" />
          </Button>
        </PopoverTrigger>
        <PopoverContent className="w-full p-0" align="start">
          <Command>
            <CommandInput placeholder={t("searchModel")} value={search} onValueChange={setSearch} />
            <CommandList>
              <CommandEmpty>{t("noModels")}</CommandEmpty>
              <CommandGroup>
                {modelNames.map((name) => (
                  <CommandItem key={name} value={name} onSelect={() => toggle(name)}>
                    <Check className={`mr-2 size-4 ${selected.has(name) ? "opacity-100" : "opacity-0"}`} />
                    {name}
                  </CommandItem>
                ))}
              </CommandGroup>
            </CommandList>
          </Command>
        </PopoverContent>
      </Popover>
      {selected.size > 0 && (
        <div className="flex flex-wrap gap-1">
          {props.value.map((name) => (
            <Badge key={name} variant="secondary" className="cursor-pointer" onClick={() => toggle(name)}>
              {name} &times;
            </Badge>
          ))}
        </div>
      )}
    </div>
  );
}

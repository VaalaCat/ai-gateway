"use client";

import { useTranslations } from "next-intl";
import { Plus, Trash2 } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";

const ROLES = ["user", "assistant", "system", "tool", "developer"] as const;
type Role = (typeof ROLES)[number];

interface RoleMapping {
  [key: string]: Role;
}

interface ModelMapping {
  model: string;
  mapping: RoleMapping;
}

interface RoleMappingConfig {
  default?: RoleMapping;
  models?: ModelMapping[];
}

interface RoleMappingEditorProps {
  value: string; // JSON string
  onChange: (value: string) => void;
}

function parseConfig(value: string): RoleMappingConfig {
  if (!value) return {};
  try {
    return JSON.parse(value);
  } catch {
    return {};
  }
}

function stringifyConfig(config: RoleMappingConfig): string {
  const cleaned: RoleMappingConfig = {};
  if (config.default && Object.keys(config.default).length > 0) {
    cleaned.default = config.default;
  }
  if (config.models && config.models.length > 0) {
    cleaned.models = config.models;
  }
  return Object.keys(cleaned).length > 0 ? JSON.stringify(cleaned) : "";
}

export function RoleMappingEditor({ value, onChange }: RoleMappingEditorProps) {
  const t = useTranslations("channels");
  const config = parseConfig(value);

  const updateDefaultMapping = (mapping: RoleMapping) => {
    const newConfig = { ...config, default: mapping };
    onChange(stringifyConfig(newConfig));
  };

  const updateModelMappings = (mappings: ModelMapping[]) => {
    const newConfig = { ...config, models: mappings };
    onChange(stringifyConfig(newConfig));
  };

  const defaultMapping = config.default || {};
  const modelMappings = config.models || [];

  return (
    <div className="space-y-4">
        <div className="space-y-2">
          <Label className="text-sm font-medium">{t("roleMappingDefault")}</Label>
          <div className="space-y-2">
            {Object.entries(defaultMapping).map(([from, to]) => (
              <div key={from} className="flex items-center gap-2">
                <Select
                  value={from}
                  onValueChange={(newFrom) => {
                    const newMapping = { ...defaultMapping };
                    delete newMapping[from as Role];
                    newMapping[newFrom as Role] = to;
                    updateDefaultMapping(newMapping);
                  }}
                >
                  <SelectTrigger className="flex-1 sm:w-32">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    {ROLES.map((role) => (
                      <SelectItem key={role} value={role}>
                        {role}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
                <span className="text-muted-foreground">→</span>
                <Select
                  value={to}
                  onValueChange={(newTo) => {
                    updateDefaultMapping({ ...defaultMapping, [from]: newTo as Role });
                  }}
                >
                  <SelectTrigger className="flex-1 sm:w-32">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    {ROLES.map((role) => (
                      <SelectItem key={role} value={role}>
                        {role}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
                <Button
                  variant="ghost"
                  size="icon"
                  className="size-8"
                  onClick={() => {
                    const newMapping = { ...defaultMapping };
                    delete newMapping[from as Role];
                    updateDefaultMapping(newMapping);
                  }}
                >
                  <Trash2 className="size-4" />
                </Button>
              </div>
            ))}
          </div>
          <Button
            variant="outline"
            size="sm"
            onClick={() => {
              const unmappedFrom = ROLES.find((r) => !defaultMapping[r]);
              if (unmappedFrom) {
                updateDefaultMapping({ ...defaultMapping, [unmappedFrom]: unmappedFrom });
              }
            }}
          >
            <Plus className="mr-2 size-4" />
            {t("roleMappingAdd")}
          </Button>
        </div>

        <div className="space-y-2">
          <Label className="text-sm font-medium">{t("roleMappingModels")}</Label>
          <div className="space-y-3">
            {modelMappings.map((mm, index) => (
              <div key={index} className="border rounded-md p-3 space-y-2">
                <div className="flex items-center gap-2">
                  <Input
                    value={mm.model}
                    onChange={(e) => {
                      const newMappings = [...modelMappings];
                      newMappings[index] = { ...mm, model: e.target.value };
                      updateModelMappings(newMappings);
                    }}
                    placeholder="gpt-4 or claude-*"
                    className="flex-1"
                  />
                  <Button
                    variant="ghost"
                    size="icon"
                    className="size-8"
                    onClick={() => {
                      updateModelMappings(modelMappings.filter((_, i) => i !== index));
                    }}
                  >
                    <Trash2 className="size-4" />
                  </Button>
                </div>
                {Object.entries(mm.mapping).map(([from, to]) => (
                  <div key={from} className="flex items-center gap-2 ml-2">
                    <Select
                      value={from}
                      onValueChange={(newFrom) => {
                        const newMapping = { ...mm.mapping };
                        delete newMapping[from as Role];
                        newMapping[newFrom as Role] = to;
                        const newMappings = [...modelMappings];
                        newMappings[index] = { ...mm, mapping: newMapping };
                        updateModelMappings(newMappings);
                      }}
                    >
                      <SelectTrigger className="flex-1 sm:w-32">
                        <SelectValue />
                      </SelectTrigger>
                      <SelectContent>
                        {ROLES.map((role) => (
                          <SelectItem key={role} value={role}>
                            {role}
                          </SelectItem>
                        ))}
                      </SelectContent>
                    </Select>
                    <span className="text-muted-foreground">→</span>
                    <Select
                      value={to}
                      onValueChange={(newTo) => {
                        const newMappings = [...modelMappings];
                        newMappings[index] = {
                          ...mm,
                          mapping: { ...mm.mapping, [from]: newTo as Role },
                        };
                        updateModelMappings(newMappings);
                      }}
                    >
                      <SelectTrigger className="flex-1 sm:w-32">
                        <SelectValue />
                      </SelectTrigger>
                      <SelectContent>
                        {ROLES.map((role) => (
                          <SelectItem key={role} value={role}>
                            {role}
                          </SelectItem>
                        ))}
                      </SelectContent>
                    </Select>
                    <Button
                      variant="ghost"
                      size="icon"
                      className="size-8"
                      onClick={() => {
                        const newMapping = { ...mm.mapping };
                        delete newMapping[from as Role];
                        const newMappings = [...modelMappings];
                        newMappings[index] = { ...mm, mapping: newMapping };
                        updateModelMappings(newMappings);
                      }}
                    >
                      <Trash2 className="size-4" />
                    </Button>
                  </div>
                ))}
                <Button
                  variant="outline"
                  size="sm"
                  className="ml-2"
                  onClick={() => {
                    const unmappedFrom = ROLES.find((r) => !mm.mapping[r]);
                    if (unmappedFrom) {
                      const newMappings = [...modelMappings];
                      newMappings[index] = {
                        ...mm,
                        mapping: { ...mm.mapping, [unmappedFrom]: unmappedFrom },
                      };
                      updateModelMappings(newMappings);
                    }
                  }}
                >
                  <Plus className="mr-2 size-4" />
                  {t("roleMappingAdd")}
                </Button>
              </div>
            ))}
          </div>
          <Button
            variant="outline"
            size="sm"
            onClick={() => {
              updateModelMappings([
                ...modelMappings,
                { model: "", mapping: {} },
              ]);
            }}
          >
            <Plus className="mr-2 size-4" />
            {t("roleMappingAddModel")}
          </Button>
        </div>
    </div>
  );
}

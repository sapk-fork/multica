"use client";

import { useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { UserCircle } from "lucide-react";
import { Card, CardContent } from "@multica/ui/components/ui/card";
import { Label } from "@multica/ui/components/ui/label";
import { Switch } from "@multica/ui/components/ui/switch";
import { useCurrentWorkspace } from "@multica/core/paths";
import { workspaceKeys } from "@multica/core/workspace/queries";
import { deriveGravatarSettings } from "@multica/core/gravatar/settings";
import { api } from "@multica/core/api";
import type { Workspace } from "@multica/core/types";
import { useT } from "../../i18n";

export function LabsTab() {
  const { t } = useT("settings");
  const workspace = useCurrentWorkspace();
  const qc = useQueryClient();
  const flags = deriveGravatarSettings(workspace);
  const [saving, setSaving] = useState(false);

  async function persistSetting(next: boolean) {
    if (!workspace || saving) return;
    setSaving(true);
    try {
      const merged = {
        ...((workspace.settings as Record<string, unknown>) ?? {}),
        gravatar_enabled: next,
      };
      const updated = await api.updateWorkspace(workspace.id, { settings: merged });
      qc.setQueryData(workspaceKeys.list(), (old: Workspace[] | undefined) =>
        old?.map((ws) => (ws.id === updated.id ? updated : ws)),
      );
    } catch (e) {
      toast.error(e instanceof Error ? e.message : t(($) => $.labs.toast_failed));
    } finally {
      setSaving(false);
    }
  }

  return (
    <div className="space-y-6">
      <div className="space-y-1">
        <h3 className="text-sm font-medium">{t(($) => $.labs.experiments_title)}</h3>
        <p className="text-sm text-muted-foreground">
          {t(($) => $.labs.experiments_description)}
        </p>
      </div>

      <Card>
        <CardContent className="py-4">
          <div className="flex items-start justify-between gap-4">
            <div className="flex items-start gap-3">
              <div className="rounded-md border bg-muted/50 p-2 text-muted-foreground">
                <UserCircle className="h-4 w-4" />
              </div>
              <div className="space-y-1">
                <Label htmlFor="gravatar-enabled" className="text-sm font-medium">
                  {t(($) => $.labs.gravatar_title)}
                </Label>
                <p className="text-sm text-muted-foreground">
                  {t(($) => $.labs.gravatar_description)}
                </p>
              </div>
            </div>
            <Switch
              id="gravatar-enabled"
              checked={flags.enabled}
              disabled={saving}
              onCheckedChange={persistSetting}
            />
          </div>
        </CardContent>
      </Card>
    </div>
  );
}

// 登记一个项目目录，或改一个已登记项目的位置。
import { useState } from "react";
import type { RegisteredProject } from "@bindings/internal/library";
import { useT } from "@/lib/i18n";
import { resolveLocalPath } from "@/lib/paths";
import { Modal } from "@/lib/ui";
import { pickDirectory } from "@/features/fetch/fetch-dialog";
import { Button } from "@/components/ui/button";
import { FieldError, Hint } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { registerProject, relocateProject } from "./projects";

// ProjectPathDialog 登记一个项目目录，或给出 project 时改它的位置:可直接输入路径，也可用系统目录选择框。
// 设置页的项目分区也用它。
export function ProjectPathDialog({
  home,
  project,
  onClose,
}: {
  home: string;
  project?: RegisteredProject;
  onClose: () => void;
}) {
  const t = useT().scopes;
  const [path, setPath] = useState(project?.path ?? "");
  const [working, setWorking] = useState(false);
  const resolved = resolveLocalPath(path, home);
  const submit = async () => {
    if (!resolved?.path || working) return;
    setWorking(true);
    const ok = project ? await relocateProject(project, resolved.path) : await registerProject(resolved.path);
    setWorking(false);
    if (ok) onClose();
  };
  return (
    <Modal
      title={project ? t.relocateDialog(project.name) : t.register}
      onClose={onClose}
      footer={
        <>
          <Button onClick={onClose}>{t.cancel}</Button>
          <Button variant="primary" disabled={!resolved?.path} loading={working} onClick={submit}>
            {project ? t.relocate : t.registerShort}
          </Button>
        </>
      }
    >
      <Hint>{project ? t.relocateHint : t.registerHint}</Hint>
      <div className="flex min-w-0 items-center gap-2">
        <Input
          className="grow"
          autoFocus
          placeholder={t.pathPlaceholder}
          value={path}
          onChange={(e) => setPath(e.target.value)}
          onKeyDown={(e) => e.key === "Enter" && submit()}
        />
        <Button
          onClick={async () => {
            const dir = await pickDirectory(project ? t.chooseRelocate : t.chooseRegister);
            if (dir) setPath(dir);
          }}
        >
          {t.choose}
        </Button>
      </div>
      {resolved?.error && <FieldError>{resolved.error}</FieldError>}
    </Modal>
  );
}

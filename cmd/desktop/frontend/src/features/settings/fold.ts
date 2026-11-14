// 左栏「集合」标题上一键折叠或列出技能少的来源：和设置页「通用」里的开关是同一项应用设置(fold_collections)。
// 先在快照里换好，左栏不等保存就跟着变;保存排队依次进行，每次取 Go 端现在的整组设置只换这一项，写完重读，失败时随重读退回。
import { AppService } from "@bindings/cmd/desktop";
import { queryClient, snapshotQuery } from "@/lib/library";
import { ui } from "@/lib/ui";

let saving: Promise<unknown> = Promise.resolve();

export function setFoldCollections(on: boolean) {
  const s = queryClient.getQueryData(snapshotQuery.queryKey);
  if (s) queryClient.setQueryData(snapshotQuery.queryKey, { ...s, foldCollections: on });
  saving = saving.then(() =>
    ui.run(async () => void (await AppService.SaveSettings({ ...(await AppService.Settings()), fold_collections: on })), { busyMark: false }),
  );
  return saving;
}

// foldSaved 在左栏发起的保存都写完时完成。设置页等它再读设置：否则读到的是旧值，之后整组写回时会把左栏刚改的盖掉。
export const foldSaved = () => saving;

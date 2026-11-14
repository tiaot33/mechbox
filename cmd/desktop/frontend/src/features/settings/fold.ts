// 左栏「集合」标题上一键折叠或列出技能少的来源：和设置页「通用」里的开关是同一项应用设置(fold_collections)。
// 先在快照里换好，左栏不等保存就跟着变;保存的是 Go 端现在的整组设置只换这一项，写完重读，失败时随重读退回。
import { AppService } from "@bindings/cmd/desktop";
import { queryClient, snapshotQuery } from "@/lib/library";
import { ui } from "@/lib/ui";

export async function setFoldCollections(on: boolean) {
  const s = queryClient.getQueryData(snapshotQuery.queryKey);
  if (s) queryClient.setQueryData(snapshotQuery.queryKey, { ...s, foldCollections: on });
  await ui.run(async () => void (await AppService.SaveSettings({ ...(await AppService.Settings()), fold_collections: on })), { busyMark: false });
}

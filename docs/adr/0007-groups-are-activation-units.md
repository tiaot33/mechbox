# 组是安装操作的单位

*（依据 [ADR 0014](0014-install-means-agent-directory.md)，术语于 2026-09-27 更新：activation → Install、group activation → Group install、Projection → Installation、Rewrite → Reinstall。决策不变；文件名保留旧措辞，以确保链接仍然有效。）*

**Group**（用户按场景组织的一组技能）是一个操作单位，不只是标签：一次操作即可将整个组安装到某个 Scope，或从中卸载。Group 本身只需要两张表：`groups` 和 `skill_groups`（多对多）。组是扁平的，不支持嵌套；按身份引用技能，不固定版本。整组安装／卸载如何落地，由安装层负责。

Group 与 **Collection** 相互独立：Collection 自动按 Source 聚合技能，而 Group 是按场景（例如“前端调试”）手动组织的跨 Source 技能组。手动挑选技能组成一组的目的，就是将它们作为一个单位使用；否则 Group 不会比 Collection 提供更多价值。因此，组是操作单位。

## 后果

- `groups`（id、name UNIQUE、created_at）+ `skill_groups`（skill_id、group_id）；关系为多对多，一个技能可以属于多个组。
- 组名唯一。创建／重命名时如果重名，追加 `#N`（从 2 开始取最小可用 N），区分大小写，不加额外空格。操作不会因此失败。
- 组是扁平的，组内不能再包含组。
- Group 引用技能的身份，而非特定版本（固定版本／跟随版本属于后续安装行为的决策）。
- 安装一个组时，为每个成员技能展开一个 Installation。每个 Installation 由安装关系撑着：技能的直接 Install（记录在 Installation 上），以及／或者覆盖它的 Group install。后者由成员关系和组安装记录推导，不逐 Installation 存储（见下文 2026-09-25；术语见 `CONTEXT.md`）。
- 卸载一个组只撤销该组的 Group install。只要仍有直接 Install 或其他 Group install 撑着，Installation 就保留。
- 删除组是非破坏性的：仅由该组撑着的 Installation，会在移除 Group install 之前转为直接 Install。
- **Collection** 仍通过派生得到，不单独建表；它自动按 Source 聚合，与手动按场景组织的 Group 对应。

## 2026-09-25：组安装独立记录，成员变更跟随组安装（#85）

最初，“这个组已安装到这个 Scope”只能从哪些 Installation 记录了该组推断出来。因此，成员变更后，Installation 仍会被已不再属于的组撑着，新成员也不会安装。现在 Group install 有独立的 Group × Scope 记录，由 `InstallGroup` 写入（空组也记录），由 `UninstallGroup`、`DeleteGroup` 或注销项目操作删除。覆盖某个 Installation 的组不再记录在该安装项上，而是由成员关系和这些记录推导：该技能属于该组，且该组已安装到这个 Scope。

- **Group install 覆盖当前及未来的成员。** 加入一个技能时，会将它安装到该组已安装到的每个 Scope。如果技能在某个 Scope 已有安装项，只增加 Group install，不写文件夹。加入成员没有覆盖开关，遇到未记录的文件夹总是跳过该 Scope，因为一个开关会作用于该组已安装到的所有 Scope；若要覆盖某一个 Scope，应用应展示安装计划，并仅对该 Scope 调用带 `Overwrite` 的 `InstallGroup`。
- **移出成员**会使其脱离该组的覆盖。默认情况下（`keep`），其 Installation 获得直接 Install 来替代该组的安装关系，磁盘不变；这与删除组采用相同的非破坏性规则。选择 `withdraw` 时不补充替代关系，不再有任何安装关系撑着的 Installation 会被移除。
- **冲突只跳过成员，不跳过整个组。** `InstallGroup` 和加入成员操作尽可能安装可安装的成员；目标被占用、存在未记录文件夹，或与其他成员同名且尚未安装的成员会被跳过，并逐成员报告（所有符合条件的同名成员都跳过，不采用先到先得；已安装的成员只增加 Group install）。成员关系和 Group install 仍然成立，再次安装该组可以补齐缺项。这取代了之前全成或全败的组安装：用户的意图是“这个组适用于这里”，一个成员冲突不应否决整个意图。
- **安装永远不会覆盖已有 Installation。** 在已有 Installation 的位置安装技能或组，只增加安装关系；将文件夹更新到当前版本属于独立的 Reinstall（#86），由应用触发。

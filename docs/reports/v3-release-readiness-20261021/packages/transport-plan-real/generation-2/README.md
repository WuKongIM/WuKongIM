# 真实 beta.24 二代兼容复验

相同 inputs.json 和已完整校验的 canonical tar/receipt/历史 control 未改动。修复后的 planner SHA256 `ae1ca68e28d6dcf487a2cf56af27944ae92accfa795df35a681e62b0772c3da7` 执行 exit 0，产出 plan_only；首代 exit 1 证据原样保留在父目录。

独立逐文件重新散列 tar 内所有普通文件，确认 plan 的 snapshot_objects 无缺失/额外项且 size/SHA 全匹配。filelists.xml.gz、other.xml.gz、primary.xml.gz 三个实际普通文件名同时为当前入口及代际快照 no-store，均无共享 immutable alias。控制及 source 身份来自父目录公开锚，未使用当前控制替代历史。

plan 为单代结构/bytes 计划；不证明签名、完整 GitHub provenance、控制提交来源认证、云状态、原子切换、CDN或stable可发布。无签名、payload执行或上传。完整检查计数、具体对象与摘要见 independent-verification.json，执行命令、隔离环境与前后源码摘要见 planner-command.json。大 tar 在父目录，仅本地复核使用，禁止复制仓库。

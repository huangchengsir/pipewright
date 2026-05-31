package build

// image_gc.go 是「镜像缓存自动管理」(Story 8-17 / FR-8-17):构建跑在中控机的容器 daemon 上,
// `docker run --rm` 只删容器**不删镜像** —— 重复构建会攒下大量**悬空(dangling/<none>)镜像**与层缓存,
// 磁盘随时间增长。本文件在每次构建后(尽力)清理悬空镜像,默认开启,可经 WithImageGC(false) 关。
//
// 安全边界:**只清悬空镜像**(`image prune -f`,即 dangling=true,被新构建顶替的无 tag 旧层),
// **绝不动有 tag 的镜像**(工具链缓存 node:20 等、本次 localTag 产物镜像都保留),不会误删在用镜像、
// 不会破坏缓存命中。清理是 best-effort:失败只记日志,绝不影响构建成败。

import "context"

// imagePruner 是「清悬空镜像」的可选能力(shellDriver 实现)。独立于 Driver 接口,
// 避免给所有 Driver 实现/假驱动新增方法;Builder 经类型断言择优调用。
type imagePruner interface {
	// PruneDangling 清理悬空(无 tag)镜像与构建层缓存(`<bin> image prune -f`)。
	// 返回是否成功执行(退出码 0)。逐行输出经 onLine。
	PruneDangling(ctx context.Context, onLine func(stream, line string)) (ok bool, err error)
}

// PruneDangling 实现 imagePruner:`<bin> image prune -f`(只清 dangling,不碰有 tag 镜像)。
func (d *shellDriver) PruneDangling(ctx context.Context, onLine func(stream, line string)) (bool, error) {
	args := []string{"image", "prune", "-f"}
	emitCmd(onLine, d.bin, args)
	code, err := d.cmdr.Stream(ctx, d.bin, args, "", onLine)
	if err != nil {
		return false, err
	}
	return code == 0, nil
}

// pruneImagesBestEffort 在构建后尽力清悬空镜像(Story 8-17):driver 支持且未关 GC 时执行。
// 任何失败仅记一行日志,绝不影响构建结果(NFR-10:运维便利不能反噬核心 CI/CD)。
func (b *Builder) pruneImagesBestEffort(ctx context.Context, onLine func(stream, line string)) {
	if b.disableImageGC {
		return
	}
	pruner, ok := b.driver.(imagePruner)
	if !ok {
		return // 远程/桩等不支持本地镜像清理的驱动:跳过(不报错)。
	}
	if _, err := pruner.PruneDangling(ctx, onLine); err != nil {
		onLine(streamStderr, "镜像清理(悬空)失败,忽略不影响构建:"+err.Error())
	}
}

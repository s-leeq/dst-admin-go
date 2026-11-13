package backup

import (
	"dst-admin-go/internal/database"
	"dst-admin-go/internal/model"
	"dst-admin-go/internal/pkg/context"
	"dst-admin-go/internal/pkg/utils/dstUtils"
	"dst-admin-go/internal/pkg/utils/fileUtils"
	"dst-admin-go/internal/pkg/utils/shellUtils"
	"dst-admin-go/internal/pkg/utils/zip"
	"dst-admin-go/internal/service/archive"
	"dst-admin-go/internal/service/dstConfig"
	"dst-admin-go/internal/service/game"
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

type BackupService struct {
	archive     *archive.PathResolver
	dstConfig   dstConfig.Config
	gameProcess game.Process
}

type BackupInfo struct {
	FileName   string    `json:"fileName"`
	FileSize   int64     `json:"fileSize"`
	CreateTime time.Time `json:"createTime"`
	Time       int64     `json:"time"`
}

type BackupSnapshot struct {
	Enable       int `json:"enable"`
	Interval     int `json:"interval"`
	MaxSnapshots int `json:"maxSnapshots"`
	IsCSave      int `json:"isCSave"`
}

func NewBackupService(archive *archive.PathResolver, dstConfig dstConfig.Config, gameProcess game.Process) *BackupService {
	return &BackupService{
		archive:     archive,
		dstConfig:   dstConfig,
		gameProcess: gameProcess,
	}
}

func (b *BackupService) GetBackupList(clusterName string) []BackupInfo {
	config, err := b.dstConfig.GetDstConfig(clusterName)
	if err != nil {
		log.Println("failed to get dst config:", err)
		return []BackupInfo{}
	}
	backupPath := config.Backup
	var backupList []BackupInfo

	if !fileUtils.Exists(backupPath) {
		return backupList
	}
	//获取文件或目录相关信息
	fileInfoList, err := ioutil.ReadDir(backupPath)
	if err != nil {
		log.Panicln(err)
	}

	for _, file := range fileInfoList {
		if file.IsDir() {
			continue
		}
		suffix := filepath.Ext(file.Name())
		if suffix == ".zip" || suffix == ".tar" {
			backup := BackupInfo{
				FileName:   file.Name(),
				FileSize:   file.Size(),
				CreateTime: file.ModTime(),
				Time:       file.ModTime().Unix(),
			}
			backupList = append(backupList, backup)
		}
	}

	return backupList

}

func (b *BackupService) RenameBackup(ctx *gin.Context, fileName, newName string) {
	clusterName := context.GetClusterName(ctx)
	config, err := b.dstConfig.GetDstConfig(clusterName)
	if err != nil {
		log.Println("failed to get dst config:", err)
		return
	}
	backupPath := config.Backup
	err = fileUtils.Rename(filepath.Join(backupPath, fileName), filepath.Join(backupPath, newName))
	if err != nil {
		return
	}
}

func (b *BackupService) DeleteBackup(ctx *gin.Context, fileNames []string) {

	clusterName := context.GetClusterName(ctx)
	config, err := b.dstConfig.GetDstConfig(clusterName)
	if err != nil {
		log.Println("failed to get dst config:", err)
		return
	}
	backupPath := config.Backup
	for _, fileName := range fileNames {
		filePath := filepath.Join(backupPath, fileName)
		if !fileUtils.Exists(filePath) {
			continue
		}
		err := fileUtils.DeleteFile(filePath)
		if err != nil {
			return
		}
	}

}

func (b *BackupService) RestoreBackup(ctx *gin.Context, backupName string) error {
	clusterName := context.GetClusterName(ctx)
	if strings.TrimSpace(clusterName) == "" {
		return errors.New("缺少集群名称，已拒绝恢复（请求头需携带 Cluster）")
	}
	config, err := b.dstConfig.GetDstConfig(clusterName)
	if err != nil {
		return fmt.Errorf("读取集群配置失败: %w", err)
	}
	if strings.TrimSpace(backupName) == "" {
		return errors.New("缺少备份文件名")
	}
	if strings.ContainsAny(backupName, `/\`) || backupName == "." || backupName == ".." {
		return fmt.Errorf("非法的备份文件名: %s", backupName)
	}
	filePath := filepath.Join(config.Backup, backupName)
	if !fileUtils.Exists(filePath) {
		return fmt.Errorf("备份不存在: %s", backupName)
	}

	clusterPath := b.archive.ClusterPath(clusterName)
	basePath := b.archive.KleiBasePath(clusterName)
	// 双保险：ClusterPath 一旦退化成克雷根目录，删除会清掉所有集群的存档
	if filepath.Clean(clusterPath) == filepath.Clean(basePath) {
		return fmt.Errorf("拒绝恢复到集群根目录: %s", clusterPath)
	}

	// 删除前先把现有存档整体移到备份目录；移动失败则中止，避免「先删后解压失败」丢档
	if fileUtils.Exists(clusterPath) {
		safePath := filepath.Join(config.Backup,
			"pre-restore-"+time.Now().Format("20060102-150405")+"-"+clusterName)
		if err := os.Rename(clusterPath, safePath); err != nil {
			return fmt.Errorf("暂存现有存档失败，已中止恢复: %w", err)
		}
		log.Println("已把现有存档暂存到", safePath)
	}
	if err := os.MkdirAll(clusterPath, 0o755); err != nil {
		return fmt.Errorf("创建集群目录失败: %w", err)
	}

	log.Println("正在恢复存档", filePath, "->", clusterPath)
	if err := zip.Unzip3(filePath, clusterPath); err != nil {
		return fmt.Errorf("解压失败: %w", err)
	}

	// 装配 mod（失败不影响本次恢复）
	if modoverride, err := fileUtils.ReadFile(b.archive.ModoverridesPath(clusterName, "Master")); err != nil {
		log.Println("读取模组失败", err)
	} else if cfg, err := b.dstConfig.GetDstConfig(clusterName); err == nil {
		if err := dstUtils.DedicatedServerModsSetup(cfg, modoverride); err != nil {
			log.Println("装配模组失败", err)
		}
	}
	return nil
}

func (b *BackupService) CreateBackup(clusterName, backupName string) {

	config, err := b.dstConfig.GetDstConfig(clusterName)
	if err != nil {
		log.Println("failed to get dst config:", err)
		return
	}
	backupPath := config.Backup

	// 执行 CSave 命令
	err = b.gameProcess.Command(clusterName, "Master", "c_save()")
	if err != nil {
		log.Println("CSave command error:", err)
	}

	// 等待保存完成
	time.Sleep(2 * time.Second)

	src := b.archive.ClusterPath(clusterName)
	if !fileUtils.Exists(backupPath) {
		log.Panicln("backup path is not exists")
	}
	if backupName == "" {
		backupName = b.GenGameBackUpName(clusterName)
	}
	dst := filepath.Join(backupPath, backupName)
	log.Println("src", src, dst)
	err = zip.Zip(src, dst)
	if err != nil {
		log.Panicln("create backup error", err)
	}
	log.Println("创建备份成功")
}

func (b *BackupService) DownloadBackup(c *gin.Context) {
	fileName := c.Query("fileName")

	clusterName := c.GetHeader("level")
	config, err := b.dstConfig.GetDstConfig(clusterName)
	if err != nil {
		log.Println("failed to get dst config:", err)
		return
	}

	filePath := filepath.Join(config.Backup, fileName)
	//打开文件
	_, err = os.Open(filePath)
	//非空处理
	if err != nil {
		log.Panicln("download filePath error", err)
	}
	c.Header("Content-Type", "application/octet-stream")
	c.Header("Content-Disposition", "attachment; filename="+fileName)
	c.Header("Content-Transfer-Encoding", "binary")
	// c.Header("Content-Length", strconv.FormatInt(f.Size(), 10))
	c.File(filePath)
}

func (b *BackupService) UploadBackup(c *gin.Context) error {
	file, err := c.FormFile("file")
	if err != nil || file == nil {
		return fmt.Errorf("缺少 file 字段: %w", err)
	}
	if strings.ContainsAny(file.Filename, `/\`) {
		return fmt.Errorf("非法的文件名: %s", file.Filename)
	}
	clusterName := context.GetClusterName(c)
	config, err := b.dstConfig.GetDstConfig(clusterName)
	if err != nil {
		return fmt.Errorf("读取集群配置失败: %w", err)
	}
	if err := os.MkdirAll(config.Backup, 0o755); err != nil {
		return fmt.Errorf("创建备份目录失败: %w", err)
	}
	dst := filepath.Join(config.Backup, file.Filename)
	if fileUtils.Exists(dst) {
		// 之前这里用 log.Panicln，前端只看到 HTTP 200，会把失败当成功
		return fmt.Errorf("备份已存在: %s（请先删除或改名后再上传）", file.Filename)
	}
	if err := c.SaveUploadedFile(file, dst); err != nil {
		return fmt.Errorf("保存上传文件失败: %w", err)
	}
	log.Println("已保存上传的备份", dst)
	return nil
}

func (b *BackupService) ScheduleBackupSnapshots() {
	log.Println("开始创建快照")
	for {
		db := database.Db
		snapshot := model.BackupSnapshot{}
		db.First(&snapshot)
		if snapshot.Enable == 1 {
			if snapshot.Interval == 0 {
				snapshot.Interval = 8
			}
			if snapshot.MaxSnapshots == 0 {
				snapshot.MaxSnapshots = 6
			}
			time.Sleep(time.Duration(snapshot.Interval) * time.Minute)

			// 定时创建备份,每隔 x 分钟备份一次
			// Get the first available cluster config (or "MyCluster" as default)
			clusterName := "MyCluster"
			config, err := b.dstConfig.GetDstConfig(clusterName)
			if err != nil {
				log.Println("failed to get dst config for snapshot:", err)
				continue
			}
			if config.Cluster != "" {
				snapshotPrefix := "(snapshot)"
				if snapshot.IsCSave == 1 {
					// 执行 CSave 命令
					err := b.gameProcess.Command(config.Cluster, "Master", "c_save()")
					if err != nil {
						log.Println("CSave command error:", err)
					}
					// 等待保存完成
					time.Sleep(2 * time.Second)
				}
				b.CreateSnapshotBackup(snapshotPrefix, config.Cluster)
				// 删除快照
				b.DeleteBackupSnapshots(snapshotPrefix, snapshot.MaxSnapshots, config.Cluster, config.Backup)
			}
		} else {
			time.Sleep(1 * time.Minute)
		}
	}
}

func sumMd5(filePath string) string {
	// find save -type f -exec md5sum {} \; | awk '{print $1}' | sort | md5sum
	comamd := "find " + filePath + " -type f -exec md5sum {} \\; | awk '{print $1}' | sort | md5sum"
	info, err := shellUtils.ExecuteCommand(comamd)
	if err != nil {
		return ""
	}
	return info
}

func (b *BackupService) CreateSnapshotBackup(prefix, clusterName string) {

	config, err := b.dstConfig.GetDstConfig(clusterName)
	if err != nil {
		log.Println("failed to get dst config:", err)
		return
	}

	snapshotMd5FilePath := "./snapshotMd5"
	fileUtils.CreateFileIfNotExists(snapshotMd5FilePath)

	src := b.archive.ClusterPath(clusterName)
	dst := filepath.Join(config.Backup, b.GenBackUpSnapshotName(prefix, clusterName))
	log.Println("[Snapshot]正在定时创建游戏备份", "src: ", src, "dst: ", dst)
	err = zip.Zip(src, dst)
	if err != nil {
		log.Println("[Snapshot]create backup error", err)
	}
}

func (b *BackupService) DeleteBackupSnapshots(prefix string, maxSnapshots int, clusterName, backupPath string) {

	log.Println("[Snapshot]正在删除快照备份", "maxSnapshots", maxSnapshots, "clusterName: ", clusterName)

	backupList := b.GetBackupList(clusterName)
	var newBackupList []BackupInfo
	for i := range backupList {
		name := backupList[i].FileName
		if strings.HasPrefix(name, prefix) {
			newBackupList = append(newBackupList, backupList[i])
		}
	}
	if len(newBackupList) > maxSnapshots {
		deleteBackupList := newBackupList[:len(newBackupList)-maxSnapshots]
		for i := range deleteBackupList {
			filePath := filepath.Join(backupPath, deleteBackupList[i].FileName)
			log.Println("删除快照备份", filePath)
			if !fileUtils.Exists(filePath) {
				continue
			}
			err := fileUtils.DeleteFile(filePath)
			if err != nil {
				return
			}
		}
	}

}

func (b *BackupService) backupPath() string {
	// dstConfig := dstConfigUtils.GetDstConfig()
	// backupPath := dstConfig.Backup
	// if !fileUtils.Exists(backupPath) {
	// 	log.Panicln("backup path is not exists")
	// }
	// return backupPath
	return ""
}

var SeasonMap = map[string]string{
	"spring": "春天",
	"summer": "夏天",
	"autumn": "秋天",
	"winter": "冬天",
}

// GenGameBackUpName 备份名称增加存档信息如  猜猜我是谁的世界-10天-spring-1-20-2023071415
func (b *BackupService) GenGameBackUpName(clusterName string) string {
	// 简化实现，使用时间戳和集群名称
	backupName := time.Now().Format("2006年01月02日15点04分05秒") + "_" + clusterName + ".zip"

	return backupName
}

func (b *BackupService) GenBackUpSnapshotName(prefix, clusterName string) string {
	backupName := b.GenGameBackUpName(clusterName)
	backupName = prefix + backupName
	return backupName
}

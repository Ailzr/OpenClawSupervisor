package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"
)

// ---------- 工具函数 ----------

func getOpenClawConfigPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".openclaw", "openclaw.json")
}

func checkNodeJS() (bool, string) {
	cmd := exec.Command("node", "--version")
	output, err := cmd.Output()
	if err != nil {
		return false, ""
	}
	return true, string(output)
}

func checkOpenClawCLI() (bool, string) {
	cmd := exec.Command("openclaw", "--version")
	output, err := cmd.Output()
	if err != nil {
		return false, ""
	}
	return true, string(output)
}

// findNPM 返回 npm 的完整路径，依次尝试 PATH 和常见安装位置
func findNPM() string {
	if p, err := exec.LookPath("npm"); err == nil {
		return p
	}
	for _, dir := range []string{
		os.ExpandEnv("$ProgramFiles\\nodejs\\npm.cmd"),
		os.ExpandEnv("$ProgramFiles(x86)\\nodejs\\npm.cmd"),
		os.ExpandEnv("$LOCALAPPDATA\\Programs\\nodejs\\npm.cmd"),
	} {
		if _, err := os.Stat(dir); err == nil {
			return dir
		}
	}
	return ""
}

func configFileExists() bool {
	_, err := os.Stat(getOpenClawConfigPath())
	return err == nil
}

func cmdError(label string, err error, output []byte) string {
	outStr := string(output)
	if outStr == "" {
		outStr = "(无输出)"
	}
	return fmt.Sprintf("%s: %v\n\n%s", label, err, outStr)
}

func applyProviderConfig(providerID, baseURL, apiKey, defaultModel string) error {
	patch := map[string]interface{}{
		"models": map[string]interface{}{
			"providers": map[string]interface{}{
				providerID: map[string]interface{}{
					"api":        "openai-completions",
					"auth":       "api-key",
					"authHeader": false,
					"baseUrl":    baseURL,
					"apiKey":     apiKey,
				},
			},
		},
	}

	// 如果填了默认模型，一并写入 agents.defaults.model
	if defaultModel != "" {
		patch["agents"] = map[string]interface{}{
			"defaults": map[string]interface{}{
				"model": defaultModel,
			},
		}
	}

	patchJSON, err := json.Marshal(patch)
	if err != nil {
		return fmt.Errorf("序列化配置失败: %w", err)
	}

	cmd := exec.Command("openclaw", "config", "patch", "--stdin")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("创建管道失败: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("启动 config patch 失败: %w", err)
	}

	_, writeErr := stdin.Write(patchJSON)
	stdin.Close()

	if writeErr != nil {
		_ = cmd.Wait()
		return fmt.Errorf("写入 patch 失败: %w", writeErr)
	}

	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("应用配置失败: %w", err)
	}

	return nil
}

// ensureGatewayAuth 生成随机 token 并写入 OpenClaw 网关认证配置
// 返回 (token, webURL) 供安装向导展示
func ensureGatewayAuth() (token string, webURL string, err error) {
	// 生成 32 字节随机 token
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", "", fmt.Errorf("生成随机 token 失败: %w", err)
	}
	token = hex.EncodeToString(raw)

	port := getGatewayPort()

	patch := map[string]interface{}{
		"gateway": map[string]interface{}{
			"auth": map[string]interface{}{
				"mode":  "token",
				"token": token,
			},
		},
	}

	patchJSON, err := json.Marshal(patch)
	if err != nil {
		return "", "", fmt.Errorf("序列化失败: %w", err)
	}

	cmd := exec.Command("openclaw", "config", "patch", "--stdin")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return "", "", fmt.Errorf("创建管道失败: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return "", "", fmt.Errorf("启动 config patch 失败: %w", err)
	}
	stdin.Write(patchJSON)
	stdin.Close()

	if err := cmd.Wait(); err != nil {
		return "", "", fmt.Errorf("写入认证配置失败: %w", err)
	}

	webURL = fmt.Sprintf("http://localhost:%d/?token=%s", port, token)
	return token, webURL, nil
}

// getGatewayPort 从 OpenClaw 配置读取端口，默认 18789
func getGatewayPort() int {
	configPath := getOpenClawConfigPath()
	data, err := os.ReadFile(configPath)
	if err != nil {
		return 18789
	}
	var cfg struct {
		Gateway struct {
			Port int `json:"port"`
		} `json:"gateway"`
	}
	if json.Unmarshal(data, &cfg) == nil && cfg.Gateway.Port > 0 {
		return cfg.Gateway.Port
	}
	return 18789
}

// autoInstallNodeJS 下载 Node.js MSI 到临时目录并返回路径
// 不执行静默安装，交给用户手动操作
// onProgress 用于实时更新 UI 状态（可选，nil 则跳过）
func autoInstallNodeJS(logChan chan string, onProgress func(string)) (msiPath string, err error) {
	setProgress := func(msg string) {
		safelog(logChan, "[Setup] "+msg)
		if onProgress != nil {
			onProgress(msg)
		}
	}

	// 方法 1: winget（如果可用且成功，直接完成）
	if _, lookErr := exec.LookPath("winget"); lookErr == nil {
		setProgress("尝试 winget 安装 Node.js...")
		cmd := exec.Command("winget", "install", "OpenJS.NodeJS.LTS",
			"--silent", "--accept-package-agreements")
		output, cmdErr := cmd.CombinedOutput()
		if cmdErr == nil {
			setProgress(fmt.Sprintf("winget 安装成功: %s", string(output)))
			return "", nil
		}
		safelog(logChan, fmt.Sprintf("[Setup] winget 失败: %v\n%s", cmdErr, string(output)))
	}

	// 方法 2: 下载 MSI 到临时目录（WebClient 比 Invoke-WebRequest 快很多）
	setProgress("正在获取最新 LTS 版本号...")
	psGetVersion := "try { " +
		"  $json = Invoke-RestMethod -Uri 'https://nodejs.org/dist/index.json' -TimeoutSec 30; " +
		"  ($json | Where-Object { $_.lts -ne $false } | Select-Object -First 1).version " +
		"} catch { 'v22.12.0' }"
	verCmd := exec.Command("powershell", "-NoProfile", "-Command", psGetVersion)
	verOut, verErr := verCmd.Output()
	ltsVersion := "v22.12.0"
	if verErr == nil && len(verOut) > 0 {
		ltsVersion = string(verOut)
		ltsVersion = ltsVersion[:len(ltsVersion)-2] // trim "\r\n"
	}
	setProgress("Node.js 版本: " + ltsVersion)

	msiName := "node-" + ltsVersion + "-x64.msi"
	msiPath = os.ExpandEnv("$TEMP\\" + msiName)
	url := "https://nodejs.org/dist/" + ltsVersion + "/" + msiName

	setProgress("正在下载 " + msiName + " (约30MB，WebClient 加速)...")
	// WebClient.DownloadFile 比 Invoke-WebRequest 快 3-5 倍
	psDownload := "$url = '" + url + "'; " +
		"$out = '" + msiPath + "'; " +
		"Write-Output 'DOWNLOAD_START'; " +
		"(New-Object System.Net.WebClient).DownloadFile($url, $out); " +
		"Write-Output 'DOWNLOAD_DONE'"

	dlCmd := exec.Command("powershell", "-NoProfile", "-ExecutionPolicy", "Bypass", "-Command", psDownload)

	stdout, pipeErr := dlCmd.StdoutPipe()
	if pipeErr != nil {
		return "", fmt.Errorf("创建管道失败: %w", pipeErr)
	}
	dlCmd.Stderr = dlCmd.Stdout

	if startErr := dlCmd.Start(); startErr != nil {
		return "", fmt.Errorf("启动下载失败: %w", startErr)
	}

	outputDone := make(chan struct{})
	var allOutput []byte
	go func() {
		buf := make([]byte, 256)
		for {
			n, readErr := stdout.Read(buf)
			if n > 0 {
				chunk := buf[:n]
				allOutput = append(allOutput, chunk...)
				line := string(chunk)
				safelog(logChan, "[Setup] "+line)
				if onProgress != nil {
					if contains(line, "DOWNLOAD_START") {
						onProgress("正在下载 Node.js 安装包...")
					} else if contains(line, "DOWNLOAD_DONE") {
						onProgress("下载完成")
					}
				}
			}
			if readErr != nil {
				break
			}
		}
		close(outputDone)
	}()

	waitErr := dlCmd.Wait()
	<-outputDone

	if waitErr != nil {
		return "", fmt.Errorf("下载失败: %v\n\n%s", waitErr, string(allOutput))
	}

	// 验证文件存在
	if _, statErr := os.Stat(msiPath); statErr != nil {
		return "", fmt.Errorf("下载的文件不存在: %s", msiPath)
	}

	setProgress("下载完成: " + msiPath)
	return msiPath, nil
}

// verifyNodeJSInstall 验证 Node.js 是否真正安装成功（检查实际文件，不依赖 PATH）
func verifyNodeJSInstall() (bool, string) {
	// 先通过 PATH 检测
	if ok, ver := checkNodeJS(); ok {
		return true, ver
	}

	// PATH 没刷出来，检查常见安装位置
	locations := []string{
		os.ExpandEnv("$ProgramFiles\\nodejs\\node.exe"),
		os.ExpandEnv("$ProgramFiles(x86)\\nodejs\\node.exe"),
		os.ExpandEnv("$LOCALAPPDATA\\Programs\\nodejs\\node.exe"),
	}
	for _, loc := range locations {
		if _, err := os.Stat(loc); err == nil {
			return true, loc
		}
	}
	return false, ""
}

// restartApp 重启应用程序
func restartApp() {
	exe, err := os.Executable()
	if err != nil {
		return
	}
	cmd := exec.Command(exe)
	cmd.Start()
	os.Exit(0)
}

// ---------- 安装向导 ----------

func InstallWizard(w fyne.Window, logChan chan string) fyne.CanvasObject {
	statusNode := widget.NewLabel("等待检测...")
	statusCLI := widget.NewLabel("等待检测...")
	statusSetup := widget.NewLabel("等待检测...")
	prepProgress := widget.NewLabel("")

	prepBtn := widget.NewButton("开始准备环境", nil)
	prepBtn.Importance = widget.HighImportance

	nodeProgressBar := widget.NewProgressBarInfinite()
	nodeProgressBar.Hide()

	step1 := container.NewVBox(
		widget.NewLabelWithStyle("环境准备", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		widget.NewLabel("安装程序将自动检测并安装所需环境，无需手动操作。"),
		widget.NewSeparator(),
		statusNode,
		nodeProgressBar,
		statusCLI,
		statusSetup,
		prepProgress,
		prepBtn,
	)

	providerIDEntry := widget.NewEntry()
	providerIDEntry.SetPlaceHolder("例如: deepseek, openai, anthropic...")

	baseURLEntry := widget.NewEntry()
	baseURLEntry.SetPlaceHolder("例如: https://api.deepseek.com")

	apiKeyEntry := widget.NewPasswordEntry()
	apiKeyEntry.SetPlaceHolder("输入你的 API Key...")

	modelEntry := widget.NewEntry()
	modelEntry.SetPlaceHolder("例如: deepseek-v4-flash, gpt-4o...")

	fetchModelsBtn := widget.NewButton("从 API 拉取模型列表", nil)
	fetchModelsBtn.Importance = widget.LowImportance

	// 常见供应商预设
	type providerPreset struct {
		name, id, baseURL, defaultModel string
	}
	presets := []providerPreset{
		{"DeepSeek", "deepseek", "https://api.deepseek.com", "deepseek-v4-flash"},
		{"OpenAI", "openai", "https://api.openai.com/v1", "gpt-4o"},
		{"Anthropic", "anthropic", "https://api.anthropic.com/v1", "claude-sonnet-4-20250514"},
		{"阿里云百炼 (DashScope)", "dashscope", "https://dashscope.aliyuncs.com/compatible-mode/v1", "qwen-plus"},
		{"火山引擎 (豆包/DeepSeek)", "ark", "https://ark.cn-beijing.volces.com/api/v3", "deepseek-v4-flash"},
		{"硅基流动 (SiliconFlow)", "siliconflow", "https://api.siliconflow.cn/v1", "deepseek-ai/DeepSeek-V3"},
		{"智谱 (Zhipu/GLM)", "zhipu", "https://open.bigmodel.cn/api/paas/v4", "glm-4-plus"},
		{"Moonshot (Kimi)", "moonshot", "https://api.moonshot.cn/v1", "moonshot-v1-8k"},
	}
	presetNames := make([]string, len(presets))
	for i, p := range presets {
		presetNames[i] = p.name
	}
	providerSelect := widget.NewSelect(presetNames, func(selected string) {
		for _, p := range presets {
			if p.name == selected {
				providerIDEntry.SetText(p.id)
				baseURLEntry.SetText(p.baseURL)
				modelEntry.SetText(p.defaultModel)
				break
			}
		}
	})
	providerSelect.PlaceHolder = "选择供应商（自动填入 ID、地址和默认模型）..."

	fetchModelsBtn.OnTapped = func() {
		baseURL := baseURLEntry.Text
		apiKey := apiKeyEntry.Text
		if baseURL == "" || apiKey == "" {
			dialog.ShowError(fmt.Errorf("请先填写 Base URL 和 API Key"), w)
			return
		}
		go func() {
			fetchModelsBtn.Disable()
			defer fetchModelsBtn.Enable()

			req, _ := http.NewRequest("GET", strings.TrimRight(baseURL, "/")+"/models", nil)
			req.Header.Set("Authorization", "Bearer "+apiKey)
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				fyne.Do(func() { dialog.ShowError(fmt.Errorf("请求失败: %v", err), w) })
				return
			}
			defer resp.Body.Close()

			var result struct {
				Data []struct {
					ID string `json:"id"`
				} `json:"data"`
			}
			if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
				fyne.Do(func() { dialog.ShowError(fmt.Errorf("解析模型列表失败: %v", err), w) })
				return
			}

			modelIDs := make([]string, 0, len(result.Data))
			for _, m := range result.Data {
				modelIDs = append(modelIDs, m.ID)
			}
			if len(modelIDs) == 0 {
				fyne.Do(func() { dialog.ShowInformation("模型列表", "未获取到任何模型，请检查 API Key 和 Base URL。", w) })
				return
			}

			fyne.Do(func() {
				dialog.ShowCustomConfirm("选择默认模型", "确定", "取消",
					widget.NewSelect(modelIDs, func(selected string) {
						modelEntry.SetText(selected)
					}),
					func(confirmed bool) {}, w)
			})
		}()
	}

	step2 := container.NewVBox(
		widget.NewLabelWithStyle("配置模型提供商", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		widget.NewLabel("选择供应商或手动输入。与 openclaw configure 行为一致。"),
		widget.NewSeparator(),
		providerSelect,
		widget.NewSeparator(),
		widget.NewForm(
			widget.NewFormItem("Provider ID", providerIDEntry),
			widget.NewFormItem("Base URL", baseURLEntry),
			widget.NewFormItem("API Key", apiKeyEntry),
			widget.NewFormItem("Default Model", container.NewBorder(nil, nil, nil, fetchModelsBtn, modelEntry)),
		),
	)

	step3 := container.NewVBox(
		widget.NewLabelWithStyle("安装完成", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		widget.NewLabel("OpenClaw 已准备就绪！现在可以启动服务了。"),
		widget.NewSeparator(),
		widget.NewLabel(fmt.Sprintf("配置文件: %s", getOpenClawConfigPath())),
	)

	allSteps := []fyne.CanvasObject{step1, step2, step3}
	contentStack := container.NewMax(allSteps...)
	currentStep := 0

	prevBtn := widget.NewButton("< 上一步", nil)
	nextBtn := widget.NewButton("下一步 >", nil)
	finishBtn := widget.NewButton("完成安装", nil)
	finishBtn.Hide()

	titleLabel := widget.NewLabelWithStyle("Step 1/3: 环境准备", fyne.TextAlignCenter, fyne.TextStyle{Bold: true})
	navBar := container.NewHBox(prevBtn, nextBtn, finishBtn)

	showStep := func(n int) {
		for i, s := range allSteps {
			if i == n {
				s.Show()
			} else {
				s.Hide()
			}
		}
		currentStep = n
		titles := []string{"Step 1/3: 环境准备", "Step 2/3: 配置提供商", "Step 3/3: 完成"}
		titleLabel.SetText(titles[n])
		prevBtn.Disable()
		if n > 0 {
			prevBtn.Enable()
		}
		if n == len(allSteps)-1 {
			nextBtn.Hide()
			finishBtn.Show()
		} else {
			nextBtn.Show()
			finishBtn.Hide()
		}
	}

	prevBtn.OnTapped = func() {
		if currentStep > 0 {
			showStep(currentStep - 1)
		}
	}
	nextBtn.OnTapped = func() {
		if currentStep < len(allSteps)-1 {
			showStep(currentStep + 1)
		}
	}

	// ======== Step 1: 自动准备环境 ========
	prepBtn.OnTapped = func() {
		prepBtn.Disable()
		prepProgress.SetText("正在检测并安装环境...")

		go func() {
			defer func() {
				if r := recover(); r != nil {
					safelog(logChan, fmt.Sprintf("[Panic] 环境准备: %v", r))
					fyne.Do(func() {
						prepBtn.Enable()
						prepProgress.SetText("发生未预期错误，请查看日志")
						dialog.ShowError(fmt.Errorf("环境准备出错:\n\n%v\n\n请查看监控面板日志。", r), w)
					})
				}
			}()

			// 给 Fyne 事件循环足够时间稳定，并确保配置文件存在
			time.Sleep(500 * time.Millisecond)
			SaveConfig(LoadConfig())

			// 后续步骤（OpenClaw CLI + setup），作为闭包可在多处调用
			continueAfterNode := func() {
				// OpenClaw CLI
				fyne.Do(func() { statusCLI.SetText("正在检查 OpenClaw CLI...") })
				ok, ver := checkOpenClawCLI()
				if !ok {
					// openclaw 不在 PATH，尝试用完整路径安装
					npmPath := findNPM()
					if npmPath == "" {
						// npm 也找不到 → 新安装的 Node.js PATH 未刷新，重启
						safelog(logChan, "[Setup] npm 不在 PATH，Node.js 刚安装需重启加载环境变量")
						fyne.Do(func() {
							statusCLI.SetText("[!] Node.js 环境变量未生效，正在重启应用...")
							prepProgress.SetText("正在重启...")
						})
						time.Sleep(1500 * time.Millisecond)
						restartApp()
						return
					}

					fyne.Do(func() { statusCLI.SetText("[X] 未检测到 openclaw，正在安装（" + npmPath + "）...") })
					safelog(logChan, "[Setup] openclaw CLI 未安装，执行 "+npmPath+" install -g openclaw")
					cmd := exec.Command(npmPath, "install", "-g", "openclaw")
					output, err := cmd.CombinedOutput()
					if err != nil {
						errMsg := cmdError("npm install openclaw", err, output)
						safelog(logChan, "[Setup] openclaw 安装失败: "+errMsg)
						fyne.Do(func() {
							statusCLI.SetText("[X] openclaw 安装失败")
							prepProgress.SetText("环境准备失败")
							prepBtn.Enable()
							dialog.ShowError(fmt.Errorf("OpenClaw CLI 安装失败:\n\n%s", errMsg), w)
						})
						return
					}
					ok, ver = checkOpenClawCLI()
					if !ok {
						// 装好了但不在 PATH → 重启加载环境变量
						safelog(logChan, "[Setup] openclaw 已安装但需重启加载 PATH")
						fyne.Do(func() {
							statusCLI.SetText("[!] OpenClaw 已安装但需重启加载 PATH")
							prepProgress.SetText("正在重启...")
						})
						time.Sleep(1500 * time.Millisecond)
						restartApp()
						return
					}
					fyne.Do(func() { statusCLI.SetText("[OK] OpenClaw CLI: " + ver) })
				} else {
					fyne.Do(func() { statusCLI.SetText("[OK] OpenClaw CLI: " + ver) })
				}

				// openclaw setup
				fyne.Do(func() { statusSetup.SetText("正在初始化配置...") })
				if !configFileExists() {
					fyne.Do(func() { statusSetup.SetText("正在运行 openclaw setup...") })
					safelog(logChan, "[Setup] 运行 openclaw setup")
					cmd := exec.Command("openclaw", "setup")
					output, err := cmd.CombinedOutput()
					if err != nil {
						errMsg := cmdError("openclaw setup", err, output)
						safelog(logChan, "[Setup] setup 失败: "+errMsg)
						fyne.Do(func() {
							statusSetup.SetText("[X] 初始化失败")
							prepProgress.SetText("初始化失败")
							prepBtn.Enable()
							dialog.ShowError(fmt.Errorf("openclaw setup 失败:\n\n%s", errMsg), w)
						})
						return
					}
					fyne.Do(func() {
						statusSetup.SetText(fmt.Sprintf("[OK] 配置已初始化\n%s", getOpenClawConfigPath()))
					})
				} else {
					fyne.Do(func() {
						statusSetup.SetText(fmt.Sprintf("[OK] 配置文件已存在\n%s", getOpenClawConfigPath()))
					})
				}

				// 生成网关认证 token
				safelog(logChan, "[Setup] 生成网关认证 token...")
				_, _, tokenErr := ensureGatewayAuth()
				if tokenErr != nil {
					safelog(logChan, "[Setup] 生成 token 失败: "+tokenErr.Error())
					fyne.Do(func() {
						statusSetup.SetText(statusSetup.Text + "\n[!] 认证 token 生成失败")
						prepProgress.SetText("[OK] 环境准备完成，但 token 生成失败。")
					})
				} else {
					safelog(logChan, "[Setup] 网关 token 已生成")
					fyne.Do(func() {
						prepProgress.SetText("[OK] 环境准备完成！启动 OpenClaw 后可在面板查看访问链接。")
					})
				}
			}

			// Node.js
			fyne.Do(func() { statusNode.SetText("正在检查 Node.js...") })
			ok, ver := verifyNodeJSInstall()
			if !ok {
				fyne.Do(func() {
					statusNode.SetText("正在准备安装 Node.js...")
					nodeProgressBar.Show()
					nodeProgressBar.Start()
				})
				safelog(logChan, "[Setup] Node.js 未安装，开始自动安装")
				msiPath, err := autoInstallNodeJS(logChan, func(msg string) {
					fyne.Do(func() { statusNode.SetText("[...] " + msg) })
				})
				if err != nil {
					errMsg := err.Error()
					safelog(logChan, "[Setup] Node.js 安装失败: "+errMsg)
					fyne.Do(func() {
						nodeProgressBar.Hide()
						nodeProgressBar.Stop()
						statusNode.SetText("[X] Node.js 安装失败")
						prepProgress.SetText("环境准备失败")
						prepBtn.Enable()
						dialog.ShowError(fmt.Errorf("Node.js 安装失败:\n\n%s", errMsg), w)
					})
					return
				}

				fyne.Do(func() { nodeProgressBar.Hide(); nodeProgressBar.Stop() })

				// winget 直接装好了（msiPath 为空），跳过手动步骤
				if msiPath == "" {
					installed, loc := verifyNodeJSInstall()
					if installed {
						fyne.Do(func() { statusNode.SetText("[OK] Node.js 已安装: " + loc) })
						continueAfterNode()
					} else {
						fyne.Do(func() { statusNode.SetText("[!] Node.js 可能已安装，请重启程序") })
						continueAfterNode()
					}
				} else {
					// MSI 已下载，打开让用户手动安装
					fyne.Do(func() { statusNode.SetText("[...] 正在打开安装程序...") })
					exec.Command("rundll32", "url.dll,FileProtocolHandler", msiPath).Start()
					time.Sleep(500 * time.Millisecond)

					// 弹窗询问用户是否安装成功
					fyne.Do(func() {
						dialog.ShowConfirm("Node.js 安装",
							"Node.js 安装程序已打开。\n\n请按照安装向导完成安装后，点击「是」继续。\n\n是否已成功安装 Node.js？",
							func(confirmed bool) {
								if !confirmed {
									fyne.Do(func() {
										statusNode.SetText("[X] Node.js 安装被取消")
										prepProgress.SetText("环境准备未完成")
										prepBtn.Enable()
									})
									return
								}

								// 用户确认已安装，验证
								installed, loc := verifyNodeJSInstall()
								if installed {
									fyne.Do(func() { statusNode.SetText("[OK] Node.js 已安装: " + loc) })
									go continueAfterNode()
								} else {
									safelog(logChan, "[Setup] 用户确认安装但验证失败")
									fyne.Do(func() {
										statusNode.SetText("[X] 未检测到 Node.js，请确认已正确安装")
										prepProgress.SetText("环境准备未完成")
										prepBtn.Enable()
									})
								}
							}, w)
					})
					return // 后续流程由对话框回调中的 continueAfterNode 接管
				}
			} else {
				fyne.Do(func() { statusNode.SetText("[OK] Node.js: " + ver) })
				continueAfterNode()
			}
		}()
	}

	// ======== Step 3: 完成 ========
	finishBtn.OnTapped = func() {
		providerID := providerIDEntry.Text
		baseURL := baseURLEntry.Text
		apiKey := apiKeyEntry.Text
		model := modelEntry.Text

		if providerID == "" && baseURL == "" && apiKey == "" {
			dialog.ShowInformation("安装完成",
				"OpenClaw 环境已就绪！\n\n"+
					"你跳过了提供商配置（Skip for now），\n"+
					"稍后可通过 openclaw configure 完成配置。", w)
			return
		}
		if providerID == "" {
			dialog.ShowError(fmt.Errorf("请输入 Provider ID"), w)
			return
		}
		if baseURL == "" {
			dialog.ShowError(fmt.Errorf("请输入 Base URL"), w)
			return
		}
		if apiKey == "" {
			dialog.ShowInformation("安装完成",
				fmt.Sprintf("已配置提供商 %s，但未输入 API Key。\n\n"+
					"稍后可通过 openclaw configure 补充 Key。", providerID), w)
			return
		}

		go func() {
			defer func() {
				if r := recover(); r != nil {
					safelog(logChan, fmt.Sprintf("[Panic] 写入配置: %v", r))
					fyne.Do(func() {
						dialog.ShowError(fmt.Errorf("未预期的错误:\n\n%v", r), w)
					})
				}
			}()
			if err := applyProviderConfig(providerID, baseURL, apiKey, model); err != nil {
				errMsg := err.Error()
				safelog(logChan, "[Setup] 配置写入失败: "+errMsg)
				fyne.Do(func() {
					dialog.ShowError(fmt.Errorf("配置写入失败:\n\n%s", errMsg), w)
				})
				return
			}
			fyne.Do(func() {
				dialog.ShowInformation("安装完成",
					fmt.Sprintf("提供商 %s 已配置完成！\n\n"+
						"配置文件: %s\n\n"+
						"现在可以启动 OpenClaw 了。", providerID, getOpenClawConfigPath()), w)
			})
		}()
	}

	showStep(0)

	return container.NewBorder(
		container.NewVBox(
			widget.NewLabelWithStyle("OpenClaw 安装向导", fyne.TextAlignCenter, fyne.TextStyle{Bold: true}),
			titleLabel,
			widget.NewSeparator(),
		),
		container.NewVBox(widget.NewSeparator(), navBar),
		nil, nil,
		contentStack,
	)
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && indexOf(s, substr) >= 0
}

func indexOf(s, substr string) int {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}

func extractAfter(s, marker string) string {
	idx := indexOf(s, marker)
	if idx < 0 {
		return s
	}
	return s[idx+len(marker):]
}

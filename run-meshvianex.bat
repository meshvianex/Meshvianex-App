@echo off
setlocal EnableExtensions EnableDelayedExpansion

cd /d "%~dp0"

where go >nul 2>&1
if errorlevel 1 (
    echo [ERROR] Go was not found in PATH.
    echo Install Go 1.26 or newer, then reopen this terminal.
    pause
    exit /b 1
)

set "config=examples\direct.json"
set "app_args="

:parse_args
if "%~1" == "" goto args_done
if /i "%~1" == "-config" goto config_arg
set "app_args=!app_args! %1"
shift
goto parse_args

:config_arg
if "%~2" == "" (
    echo [ERROR] -config requires a JSON configuration path.
    pause
    exit /b 1
)
set "config=%~2"
shift
shift
goto parse_args

:args_done
set "temp_config=%TEMP%\meshvianex-%RANDOM%-%RANDOM%.json"
set "ui_port="

for /f "usebackq" %%P in (`powershell.exe -NoProfile -ExecutionPolicy Bypass -Command "$ErrorActionPreference='Stop'; while($true){$p=Get-Random -Minimum 10000 -Maximum 60000; $listener=$null; try{$listener=[System.Net.Sockets.TcpListener]::new([System.Net.IPAddress]::Loopback,$p); $listener.Start(); $listener.Stop(); [Console]::WriteLine($p); break}catch{if($null -ne $listener){$listener.Stop()}}}"`) do (
    set "ui_port=%%P"
)

if not defined ui_port goto port_error

goto ports_ready

:port_error
    echo [ERROR] Could not find an available TCP port.
    pause
    exit /b 1

:ports_ready

powershell.exe -NoProfile -ExecutionPolicy Bypass -Command "$ErrorActionPreference='Stop'; $source=Get-Content -Raw -LiteralPath '%config%'; $cfg=$source|ConvertFrom-Json; if($cfg.ui.enabled -eq $true){if($null -eq $cfg.ui.config){$cfg.ui.config=[pscustomobject]@{}}; $cfg.ui.config.listen='127.0.0.1:%ui_port%'}; $json=$cfg|ConvertTo-Json -Depth 100; $utf8=New-Object System.Text.UTF8Encoding -ArgumentList $false; [System.IO.File]::WriteAllText('%temp_config%',$json,$utf8)"
if errorlevel 1 (
    echo [ERROR] Could not create the temporary configuration.
    if exist "%temp_config%" del /q "%temp_config%" >nul 2>&1
    pause
    exit /b 1
)

echo Meshvianex Web UI: http://127.0.0.1:!ui_port!
echo Starting with config: !config!

start "" powershell.exe -NoProfile -WindowStyle Hidden -ExecutionPolicy Bypass -Command "$ErrorActionPreference='SilentlyContinue'; $url='http://127.0.0.1:%ui_port%'; $port=%ui_port%; $ready=$false; for($i=0;$i -lt 120;$i++){ $client=$null; try{$client=[System.Net.Sockets.TcpClient]::new(); $client.Connect('127.0.0.1',$port); $ready=$client.Connected}catch{} finally{if($null -ne $client){$client.Dispose()}}; if($ready){break}; Start-Sleep -Milliseconds 100 }; if(-not $ready){exit 1}; $browser=$null; foreach($candidate in @('msedge.exe','chrome.exe','brave.exe','chromium.exe')){ $command=Get-Command $candidate -ErrorAction SilentlyContinue; if($null -ne $command){$browser=$command.Source; break} }; if($null -eq $browser){foreach($root in @($env:ProgramFiles,${env:ProgramFiles(x86)},$env:LocalAppData)){if([string]::IsNullOrWhiteSpace($root)){continue}; foreach($relative in @('Microsoft\Edge\Application\msedge.exe','Google\Chrome\Application\chrome.exe','BraveSoftware\Brave-Browser\Application\brave.exe','Chromium\Application\chrome.exe')){$candidatePath=Join-Path $root $relative; if(Test-Path -LiteralPath $candidatePath -PathType Leaf){$browser=$candidatePath; break}}; if($null -ne $browser){break}}}; if($null -ne $browser){$profile=Join-Path ([IO.Path]::GetTempPath()) ('meshvianex-ui-'+[guid]::NewGuid()); New-Item -ItemType Directory -Path $profile | Out-Null; try{$process=Start-Process -FilePath $browser -ArgumentList @('--app='+$url,'--new-window','--user-data-dir='+$profile) -PassThru; Wait-Process -Id $process.Id}catch{Start-Process $url}finally{Remove-Item -LiteralPath $profile -Recurse -Force -ErrorAction SilentlyContinue}}else{Start-Process $url}"
set "MESHVIANEX_UI_LAUNCHER=bat"
set "MESHVIANEX_HTTP_PROXY_LISTEN=127.0.0.1:0"
go run .\cmd\meshvianex -config "%temp_config%" !app_args!
set "exit_code=!ERRORLEVEL!"

if exist "%temp_config%" del /q "%temp_config%" >nul 2>&1

if not "!exit_code!" == "0" pause

endlocal & exit /b !exit_code!

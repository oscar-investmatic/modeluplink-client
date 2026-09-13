#ifndef AppVersion
#error AppVersion must be supplied by windows/build.ps1
#endif
#ifndef BuildDir
#define BuildDir "..\dist\windows"
#endif
[Setup]
#ifdef SignedBuild
SignTool=ModelUplink
SignedUninstaller=yes
#endif
AppId={{0F386C10-2888-4A84-A736-4A8B4B2C7A43}
AppName=Model Uplink
AppVersion={#AppVersion}
AppPublisher=Model Uplink
AppPublisherURL=https://modeluplink.com/
AppSupportURL=https://modeluplink.com/quickstart/
AppUpdatesURL=https://modeluplink.com/download/
DefaultDirName={localappdata}\Programs\Model Uplink
PrivilegesRequired=lowest
MinVersion=10.0.22000
ArchitecturesAllowed=x64compatible
ArchitecturesInstallIn64BitMode=x64compatible
UninstallDisplayIcon={app}\modeluplink-app.exe
AppMutex=Local\ModelUplink-UI
OutputDir={#BuildDir}
OutputBaseFilename=modeluplink_{#AppVersion}_windows_amd64
Compression=lzma2
SolidCompression=yes
WizardStyle=modern
CloseApplications=yes
RestartApplications=no
DisableProgramGroupPage=yes
[Messages]
SetupAppRunningError=%1 is still running in the tray.%n%nChoose Exit from the Model Uplink tray menu, then click OK to continue, or Cancel to exit.
UninstallAppRunningError=%1 is still running in the tray.%n%nChoose Exit from the Model Uplink tray menu, then click OK to continue, or Cancel to exit.
[Files]
Source: "..\LICENSE"; DestDir: "{app}"; Flags: ignoreversion
Source: "..\NOTICE"; DestDir: "{app}"; Flags: ignoreversion
Source: "{#BuildDir}\modeluplink-app.exe"; DestDir: "{app}"; Flags: ignoreversion
Source: "{#BuildDir}\modeluplink.exe"; DestDir: "{app}"; Flags: ignoreversion
[Icons]
Name: "{userprograms}\Model Uplink"; Filename: "{app}\modeluplink-app.exe"
[Run]
Filename: "{app}\modeluplink-app.exe"; Description: "Open Model Uplink"; Flags: nowait postinstall skipifsilent
[Code]
function IsWow64Process2(Process: THandle; var ProcessMachine, NativeMachine: Word): Boolean;
  external 'IsWow64Process2@kernel32.dll stdcall';
function InitializeSetup(): Boolean;
var ProcessMachine, NativeMachine: Word;
begin
  Result := IsWow64Process2(THandle(-1), ProcessMachine, NativeMachine) and (NativeMachine = $8664);
  if not Result then MsgBox('This installer requires Windows 11 on an x86-64 computer.', mbError, MB_OK);
end;
function PrepareToInstall(var NeedsRestart: Boolean): String;
var Code: Integer; Helper: String;
begin
  Result := '';
  Helper := ExpandConstant('{app}\modeluplink.exe');
  if FileExists(Helper) then begin
    if not Exec(Helper, '_prepare-update', '', SW_HIDE, ewWaitUntilTerminated, Code) or (Code <> 0) then
      Result := 'Open Model Uplink, stop sharing and release model memory, then retry the update.';
  end;
end;
procedure CurUninstallStepChanged(CurUninstallStep: TUninstallStep);
var Code: Integer;
begin
  if CurUninstallStep = usUninstall then begin
    if not Exec(ExpandConstant('{app}\modeluplink.exe'), '_uninstall', '', SW_HIDE, ewWaitUntilTerminated, Code) or (Code <> 0) then
      RaiseException('Model Uplink could not finish stopping sharing and clearing local credentials. Open the app and stop sharing before retrying. Your models have been kept.');
  end;
end;

procedure CurStepChanged(CurStep: TSetupStep);
var Code: Integer;
begin
  if CurStep = ssPostInstall then begin
    if not Exec(ExpandConstant('{app}\modeluplink.exe'), '_finish-update', '', SW_HIDE, ewWaitUntilTerminated, Code) or (Code <> 0) then
      MsgBox('Model Uplink was installed, but sharing could not resume. Open the app to reconnect. Your settings and models have been kept.', mbError, MB_OK);
  end;
end;

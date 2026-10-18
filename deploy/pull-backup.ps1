# pull-backup.ps1: Skrip Penjadwalan Unduh Cadangan Otomatis GO BLAST ke Komputer Pemilik
# Dijalankan via Windows Task Scheduler atau manual oleh pemilik di PowerShell

param (
    [string]$DestinationDir = "$HOME\Backups\goblast",
    [string]$SshHost = "vps",
    [string]$RemoteBackupPath = "/opt/goblast/backups/*.gbak"
)

$ErrorActionPreference = "Stop"

Write-Host "=== GO BLAST PULL BACKUP ===" -ForegroundColor Cyan
Write-Host "Waktu          : $(Get-Date -Format 'yyyy-MM-dd HH:mm:ss')"
Write-Host "Target Lokal   : $DestinationDir"
Write-Host "Host SSH       : $SshHost"

# 1. Pastikan folder tujuan lokal tersedia
if (-not (Test-Path $DestinationDir)) {
    New-Item -ItemType Directory -Force -Path $DestinationDir | Out-Null
    Write-Host "[OK] Membuat folder tujuan lokal: $DestinationDir" -ForegroundColor Green
}

# 2. Sinkronisasi berkas cadangan dari server VPS menggunakan SCP & kunci SSH
Write-Host "Menyinkronkan berkas cadangan terenkripsi (.gbak)..." -ForegroundColor Yellow
try {
    # Jalankan scp menggunakan host ssh yang sudah terkonfigurasi (tanpa password)
    $scpArgs = @(
        "-p",
        "-o", "BatchMode=yes",
        "-o", "ConnectTimeout=15",
        "${SshHost}:${RemoteBackupPath}",
        $DestinationDir
    )
    
    $proc = Start-Process -FilePath "scp" -ArgumentList $scpArgs -NoNewWindow -Wait -PassThru
    
    if ($proc.ExitCode -eq 0) {
        Write-Host "[SUKSES] Seluruh berkas cadangan terbaru berhasil diunduh!" -ForegroundColor Green
    } else {
        Write-Host "[PERINGATAN] SCP selesai dengan kode exit: $($proc.ExitCode) (Mungkin belum ada file .gbak di server)" -ForegroundColor Yellow
    }
} catch {
    Write-Host "[GAGAL] Terjadi kesalahan saat mengunduh cadangan: $_" -ForegroundColor Red
    exit 1
}

# 3. Tampilkan daftar cadangan lokal yang tersimpan
Write-Host ""
Write-Host "Daftar Berkas Cadangan Lokal:" -ForegroundColor Cyan
Get-ChildItem -Path $DestinationDir -Filter "*.gbak" | 
    Sort-Object LastWriteTime -Descending | 
    Select-Object Name, Length, LastWriteTime | 
    Format-Table -AutoSize

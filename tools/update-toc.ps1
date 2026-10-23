param(
  [Parameter(Mandatory=$true)][string]$Docx,
  [string]$Pdf = ""
)
$ErrorActionPreference = "Stop"
$wps = New-Object -ComObject KWPS.Application
$wps.Visible = $false
try { $wps.DisplayAlerts = 0 } catch {}
try {
  $doc = $wps.Documents.Open($Docx, $false, $false)
  # 更新所有域与目录
  try { $doc.Fields.Update() | Out-Null } catch { Write-Host "Fields.Update: $($_.Exception.Message)" }
  try {
    for ($i = 1; $i -le $doc.TablesOfContents.Count; $i++) {
      $doc.TablesOfContents.Item($i).Update()
    }
    Write-Host "TOC updated: $($doc.TablesOfContents.Count)"
  } catch { Write-Host "TOC: $($_.Exception.Message)" }
  try { $doc.Repaginate() } catch {}
  $doc.Save()
  if ($Pdf -ne "") {
    $doc.SaveAs2($Pdf, 17)
    Write-Host "PDF saved: $Pdf"
  }
  $pages = $doc.ComputeStatistics(2)
  $words = $doc.ComputeStatistics(0)
  Write-Host "pages=$pages words=$words"
  $doc.Close(0)
} finally {
  $wps.Quit()
  [System.Runtime.InteropServices.Marshal]::ReleaseComObject($wps) | Out-Null
}
Write-Host "OK"

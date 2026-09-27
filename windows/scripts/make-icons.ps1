# SPDX-FileCopyrightText: 2026 Vladislav Janeček
# SPDX-License-Identifier: GPL-3.0-or-later
#
# Renders the icon of MalachiMail.exe, a multi-size .ico, from
# docs/malachi_icon.png: the counterpart of the icon target of macos/Makefile
# (sips and iconutil), with the System.Drawing that Windows PowerShell ships.
# Run by the MalachiIcons target of Directory.Build.targets and by
# build.ps1 icons.
#
# The source keeps a transparent margin around its rounded square (a 1254 px
# canvas). The square is cut out exactly as macos/Makefile cuts it,
# `sips -c 1126 1126 --cropOffset 64 65`: sips takes sizes and offsets
# height first, so that is 1126 x 1126 px from x 65, y 64. The icon then
# fills its tile edge to edge.
#
# Sizes 16 to 96 are stored as 32-bit bitmaps with alpha, which every part of
# Windows reads; 256 as PNG, as Windows expects for the large size.
#
# Windows PowerShell 5.1 and PowerShell 7: keep this file ASCII apart from
# the header.
[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)] [string] $Source,
    [Parameter(Mandatory = $true)] [string] $OutFile
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest
Add-Type -AssemblyName System.Drawing

# Relative to the current location, which .NET's own resolution ignores.
$Source = $ExecutionContext.SessionState.Path.GetUnresolvedProviderPathFromPSPath($Source)
$OutFile = $ExecutionContext.SessionState.Path.GetUnresolvedProviderPathFromPSPath($OutFile)

$CropX = 65
$CropY = 64
$CropSize = 1126
$Sizes = @(16, 20, 24, 32, 40, 48, 64, 96, 256)
$PngFrom = 256

# The square, resampled to size x size pixels.
function New-Frame([System.Drawing.Bitmap] $Square, [int] $Size) {
    $frame = New-Object System.Drawing.Bitmap $Size, $Size, ([System.Drawing.Imaging.PixelFormat]::Format32bppArgb)
    $g = [System.Drawing.Graphics]::FromImage($frame)
    $attributes = New-Object System.Drawing.Imaging.ImageAttributes
    try {
        $g.CompositingMode = [System.Drawing.Drawing2D.CompositingMode]::SourceCopy
        $g.CompositingQuality = [System.Drawing.Drawing2D.CompositingQuality]::HighQuality
        $g.InterpolationMode = [System.Drawing.Drawing2D.InterpolationMode]::HighQualityBicubic
        $g.PixelOffsetMode = [System.Drawing.Drawing2D.PixelOffsetMode]::HighQuality
        $g.SmoothingMode = [System.Drawing.Drawing2D.SmoothingMode]::HighQuality
        # Mirrored edges instead of transparent ones: no faint seam along the
        # sides of the square after resampling.
        $attributes.SetWrapMode([System.Drawing.Drawing2D.WrapMode]::TileFlipXY)
        $dest = New-Object System.Drawing.Rectangle 0, 0, $Size, $Size
        $g.DrawImage($Square, $dest, 0, 0, $Square.Width, $Square.Height, [System.Drawing.GraphicsUnit]::Pixel, $attributes)
    } finally {
        $attributes.Dispose()
        $g.Dispose()
    }
    return $frame
}

function Get-PngBytes([System.Drawing.Bitmap] $Frame) {
    $stream = New-Object System.IO.MemoryStream
    try {
        $Frame.Save($stream, [System.Drawing.Imaging.ImageFormat]::Png)
        return , $stream.ToArray()
    } finally {
        $stream.Dispose()
    }
}

# A DIB as .ico stores it: BITMAPINFOHEADER with twice the height, the
# colour rows bottom-up (BGRA, straight alpha), then the 1-bit AND mask,
# set where a pixel is fully transparent, each row padded to 32 bits.
function Get-DibBytes([System.Drawing.Bitmap] $Frame) {
    $size = $Frame.Width
    $rect = New-Object System.Drawing.Rectangle 0, 0, $size, $size
    $data = $Frame.LockBits($rect, [System.Drawing.Imaging.ImageLockMode]::ReadOnly, [System.Drawing.Imaging.PixelFormat]::Format32bppArgb)
    try {
        $stride = $data.Stride
        $pixels = New-Object byte[] ($stride * $size)
        [System.Runtime.InteropServices.Marshal]::Copy($data.Scan0, $pixels, 0, $pixels.Length)
    } finally {
        $Frame.UnlockBits($data)
    }
    $rowBytes = $size * 4
    $maskStride = [int]([Math]::Floor(($size + 31) / 32) * 4)
    $mask = New-Object byte[] ($maskStride * $size)
    $stream = New-Object System.IO.MemoryStream
    $writer = New-Object System.IO.BinaryWriter $stream
    try {
        $writer.Write([UInt32]40)
        $writer.Write([Int32]$size)
        $writer.Write([Int32]($size * 2))
        $writer.Write([UInt16]1)
        $writer.Write([UInt16]32)
        $writer.Write([UInt32]0)
        $writer.Write([UInt32]($rowBytes * $size + $mask.Length))
        $writer.Write([Int32]0)
        $writer.Write([Int32]0)
        $writer.Write([UInt32]0)
        $writer.Write([UInt32]0)
        for ($y = $size - 1; $y -ge 0; $y--) {
            $writer.Write($pixels, $y * $stride, $rowBytes)
            $maskRow = ($size - 1 - $y) * $maskStride
            for ($x = 0; $x -lt $size; $x++) {
                if ($pixels[$y * $stride + $x * 4 + 3] -eq 0) {
                    $mask[$maskRow + [int][Math]::Floor($x / 8)] = $mask[$maskRow + [int][Math]::Floor($x / 8)] -bor (0x80 -shr ($x % 8))
                }
            }
        }
        $writer.Write($mask)
        $writer.Flush()
        return , $stream.ToArray()
    } finally {
        $writer.Dispose()
        $stream.Dispose()
    }
}

$sourceImage = New-Object System.Drawing.Bitmap $Source
try {
    if ($sourceImage.Width -lt $CropX + $CropSize -or $sourceImage.Height -lt $CropY + $CropSize) {
        throw "make-icons: $Source is $($sourceImage.Width) x $($sourceImage.Height) px; the crop needs $($CropX + $CropSize) x $($CropY + $CropSize)"
    }
    $cropRect = New-Object System.Drawing.Rectangle $CropX, $CropY, $CropSize, $CropSize
    $square = $sourceImage.Clone($cropRect, [System.Drawing.Imaging.PixelFormat]::Format32bppArgb)
} finally {
    $sourceImage.Dispose()
}

$images = [System.Collections.Generic.List[byte[]]]::new()
try {
    foreach ($size in $Sizes) {
        $frame = New-Frame $square $size
        try {
            if ($size -ge $PngFrom) {
                $images.Add((Get-PngBytes $frame))
            } else {
                $images.Add((Get-DibBytes $frame))
            }
        } finally {
            $frame.Dispose()
        }
    }
} finally {
    $square.Dispose()
}

# ICONDIR, one ICONDIRENTRY per image, then the images.
$ico = New-Object System.IO.MemoryStream
$writer = New-Object System.IO.BinaryWriter $ico
try {
    $writer.Write([UInt16]0)
    $writer.Write([UInt16]1)
    $writer.Write([UInt16]$Sizes.Count)
    $offset = 6 + 16 * $Sizes.Count
    for ($i = 0; $i -lt $Sizes.Count; $i++) {
        $size = $Sizes[$i]
        $dimension = $size
        if ($size -ge 256) { $dimension = 0 }
        $writer.Write([byte]$dimension)
        $writer.Write([byte]$dimension)
        $writer.Write([byte]0)
        $writer.Write([byte]0)
        $writer.Write([UInt16]1)
        $writer.Write([UInt16]32)
        $writer.Write([UInt32]$images[$i].Length)
        $writer.Write([UInt32]$offset)
        $offset += $images[$i].Length
    }
    foreach ($image in $images) {
        $writer.Write($image)
    }
    $writer.Flush()
    $bytes = $ico.ToArray()
} finally {
    $writer.Dispose()
    $ico.Dispose()
}

# Written beside the target and moved into place, so that an interrupted run
# never leaves a truncated icon that MSBuild would take as up to date.
$dir = [System.IO.Path]::GetDirectoryName($OutFile)
[System.IO.Directory]::CreateDirectory($dir) | Out-Null
$temp = "$OutFile.tmp"
[System.IO.File]::WriteAllBytes($temp, $bytes)
if ([System.IO.File]::Exists($OutFile)) {
    [System.IO.File]::Delete($OutFile)
}
[System.IO.File]::Move($temp, $OutFile)

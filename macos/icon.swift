import AppKit
import Foundation

let folder = CommandLine.arguments[1]
try FileManager.default.createDirectory(atPath: folder, withIntermediateDirectories: true)
for size in [16, 32, 128, 256, 512] {
    for scale in [1, 2] {
        let pixels = size * scale
        let image = NSImage(size: NSSize(width: pixels, height: pixels))
        image.lockFocus()
        let side = CGFloat(pixels)
        NSColor(calibratedRed: 0.055, green: 0.08, blue: 0.065, alpha: 1).setFill()
        NSBezierPath(roundedRect: NSRect(x: side * 0.04, y: side * 0.04, width: side * 0.92, height: side * 0.92), xRadius: side * 0.2, yRadius: side * 0.2).fill()
        let green = NSColor(calibratedRed: 0.66, green: 1, blue: 0.31, alpha: 1)
        green.withAlphaComponent(0.25).setStroke()
        let orbit = NSBezierPath(ovalIn: NSRect(x: side * 0.19, y: side * 0.19, width: side * 0.62, height: side * 0.62))
        orbit.lineWidth = max(1, side * 0.012); orbit.stroke()
        green.setFill()
        let plane = NSBezierPath()
        plane.move(to: NSPoint(x: side * 0.24, y: side * 0.54))
        plane.line(to: NSPoint(x: side * 0.77, y: side * 0.74))
        plane.line(to: NSPoint(x: side * 0.58, y: side * 0.23))
        plane.line(to: NSPoint(x: side * 0.47, y: side * 0.43))
        plane.close(); plane.fill()
        NSBezierPath(ovalIn: NSRect(x: side * 0.22, y: side * 0.23, width: side * 0.07, height: side * 0.07)).fill()
        image.unlockFocus()
        let bitmap = NSBitmapImageRep(data: image.tiffRepresentation!)!
        let suffix = scale == 2 ? "@2x" : ""
        try bitmap.representation(using: .png, properties: [:])!.write(to: URL(fileURLWithPath: folder).appendingPathComponent("icon_\(size)x\(size)\(suffix).png"))
    }
}

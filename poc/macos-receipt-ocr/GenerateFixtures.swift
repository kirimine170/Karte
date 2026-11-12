import AppKit
import Foundation

struct Fixture {
    let name: String
    let size: NSSize
    let rotation: CGFloat
    let fontSize: CGFloat
}

let output = CommandLine.arguments.count > 1 ? CommandLine.arguments[1] : "fixtures"
try FileManager.default.createDirectory(atPath: output, withIntermediateDirectories: true)

let receipt = [
    "架空商店 渋谷店",
    "東京都渋谷区1-2-3",
    "2026/08/13  12:34",
    "------------------------",
    "おにぎり 鮭       ¥158",
    "緑茶 500ml        ¥128",
    "りんご             ¥98",
    "------------------------",
    "小計              ¥384",
    "消費税              ¥30",
    "合計              ¥414",
    "お預り            ¥1,000",
    "お釣り             ¥586",
    "ありがとうございました"
]

let fixtures = [
    Fixture(name: "receipt-small", size: NSSize(width: 600, height: 1200), rotation: 0, fontSize: 24),
    Fixture(name: "receipt-medium", size: NSSize(width: 1200, height: 2400), rotation: 0, fontSize: 48),
    Fixture(name: "receipt-large", size: NSSize(width: 2400, height: 4800), rotation: 0, fontSize: 96),
    Fixture(name: "receipt-rotated-90", size: NSSize(width: 2400, height: 1200), rotation: 90, fontSize: 48)
]

for fixture in fixtures {
    let image = NSImage(size: fixture.size)
    image.lockFocus()
    NSColor(calibratedWhite: 0.96, alpha: 1).setFill()
    NSRect(origin: .zero, size: fixture.size).fill()
    let context = NSGraphicsContext.current!.cgContext
    context.saveGState()
    if fixture.rotation == 90 {
        context.translateBy(x: fixture.size.width / 2, y: fixture.size.height / 2)
        context.rotate(by: -.pi / 2)
        context.translateBy(x: -fixture.size.height / 2, y: -fixture.size.width / 2)
    }
    let paragraph = NSMutableParagraphStyle()
    paragraph.lineSpacing = fixture.fontSize * 0.38
    let attributes: [NSAttributedString.Key: Any] = [
        .font: NSFont.monospacedSystemFont(ofSize: fixture.fontSize, weight: .regular),
        .foregroundColor: NSColor(calibratedWhite: 0.08, alpha: 1),
        .paragraphStyle: paragraph
    ]
    let canvasWidth = fixture.rotation == 90 ? fixture.size.height : fixture.size.width
    let canvasHeight = fixture.rotation == 90 ? fixture.size.width : fixture.size.height
    NSString(string: receipt.joined(separator: "\n")).draw(
        in: NSRect(x: canvasWidth * 0.08, y: canvasHeight * 0.06,
                   width: canvasWidth * 0.84, height: canvasHeight * 0.88),
        withAttributes: attributes
    )
    context.restoreGState()
    image.unlockFocus()

    guard let tiff = image.tiffRepresentation,
          let bitmap = NSBitmapImageRep(data: tiff),
          let png = bitmap.representation(using: .png, properties: [:]) else {
        fatalError("fixture生成に失敗: \(fixture.name)")
    }
    try png.write(to: URL(fileURLWithPath: output).appendingPathComponent("\(fixture.name).png"))
}

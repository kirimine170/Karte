import AppKit
import CoreGraphics
import CoreML
import Foundation
import ImageIO
import Vision

struct OCRLine: Codable {
    let text: String
    let confidence: Float
    let x: Double
    let y: Double
    let width: Double
    let height: Double
}

struct RunResult: Codable {
    let image: String
    let width: Int
    let height: Int
    let compute: String
    let recognitionLevel: String
    let elapsedMs: Double
    let userCPUMs: Double
    let systemCPUMs: Double
    let residentBeforeMiB: Double
    let residentAfterMiB: Double
    let residentDeltaMiB: Double
    let lines: [OCRLine]
}

enum CLIError: Error, CustomStringConvertible {
    case usage(String)
    case image(String)

    var description: String {
        switch self {
        case .usage(let message), .image(let message): return message
        }
    }
}

func residentMiB() -> Double {
    var info = mach_task_basic_info()
    var count = mach_msg_type_number_t(MemoryLayout<mach_task_basic_info>.size) / 4
    let result = withUnsafeMutablePointer(to: &info) {
        $0.withMemoryRebound(to: integer_t.self, capacity: Int(count)) {
            task_info(mach_task_self_, task_flavor_t(MACH_TASK_BASIC_INFO), $0, &count)
        }
    }
    return result == KERN_SUCCESS ? Double(info.resident_size) / 1_048_576.0 : 0
}

func cpuMilliseconds() -> (user: Double, system: Double) {
    var usage = rusage()
    getrusage(RUSAGE_SELF, &usage)
    func ms(_ value: timeval) -> Double {
        Double(value.tv_sec) * 1_000 + Double(value.tv_usec) / 1_000
    }
    return (ms(usage.ru_utime), ms(usage.ru_stime))
}

func loadImage(_ path: String) throws -> CGImage {
    let url = URL(fileURLWithPath: path) as CFURL
    guard let source = CGImageSourceCreateWithURL(url, nil),
          let image = CGImageSourceCreateImageAtIndex(source, 0, nil) else {
        throw CLIError.image("画像を読み込めません: \(path)")
    }
    return image
}

func recognize(path: String, compute: String, fast: Bool) throws -> RunResult {
    let image = try loadImage(path)
    let beforeRSS = residentMiB()
    let beforeCPU = cpuMilliseconds()
    let started = ContinuousClock.now

    let request = VNRecognizeTextRequest()
    request.recognitionLevel = fast ? .fast : .accurate
    request.recognitionLanguages = ["ja-JP", "en-US"]
    request.usesLanguageCorrection = true
    if compute == "cpu" {
        if #available(macOS 14.0, *) {
            for (stage, devices) in try request.supportedComputeStageDevices {
                guard let cpu = devices.first(where: {
                    if case .cpu = $0 { return true }
                    return false
                }) else {
                    throw CLIError.usage("CPU deviceがVision stage \(stage)で利用できません")
                }
                request.setComputeDevice(cpu, for: stage)
            }
        } else {
            request.usesCPUOnly = true
        }
    }

    let handler = VNImageRequestHandler(cgImage: image, orientation: .up, options: [:])
    try handler.perform([request])
    let elapsed = started.duration(to: .now)
    let afterCPU = cpuMilliseconds()
    let afterRSS = residentMiB()

    let observations = (request.results ?? []).sorted {
        let yDistance = abs($0.boundingBox.midY - $1.boundingBox.midY)
        if yDistance > 0.015 { return $0.boundingBox.midY > $1.boundingBox.midY }
        return $0.boundingBox.minX < $1.boundingBox.minX
    }
    let lines = observations.compactMap { observation -> OCRLine? in
        guard let candidate = observation.topCandidates(1).first else { return nil }
        let box = observation.boundingBox
        return OCRLine(text: candidate.string, confidence: candidate.confidence,
                       x: box.minX, y: box.minY, width: box.width, height: box.height)
    }
    let components = elapsed.components
    let elapsedMs = Double(components.seconds) * 1_000 + Double(components.attoseconds) / 1e15
    return RunResult(
        image: path, width: image.width, height: image.height, compute: compute,
        recognitionLevel: fast ? "fast" : "accurate", elapsedMs: elapsedMs,
        userCPUMs: afterCPU.user - beforeCPU.user,
        systemCPUMs: afterCPU.system - beforeCPU.system,
        residentBeforeMiB: beforeRSS, residentAfterMiB: afterRSS,
        residentDeltaMiB: afterRSS - beforeRSS, lines: lines
    )
}

func option(_ name: String, in args: [String]) -> String? {
    guard let index = args.firstIndex(of: name), index + 1 < args.count else { return nil }
    return args[index + 1]
}

do {
    let args = Array(CommandLine.arguments.dropFirst())
    guard let image = option("--image", in: args) else {
        throw CLIError.usage("usage: receipt-ocr --image PATH [--compute auto|cpu] [--fast]")
    }
    let compute = option("--compute", in: args) ?? "auto"
    guard ["auto", "cpu"].contains(compute) else {
        throw CLIError.usage("--compute は auto または cpu を指定してください")
    }
    let result = try recognize(path: image, compute: compute, fast: args.contains("--fast"))
    let encoder = JSONEncoder()
    encoder.outputFormatting = [.prettyPrinted, .sortedKeys]
    FileHandle.standardOutput.write(try encoder.encode(result))
    FileHandle.standardOutput.write(Data("\n".utf8))
} catch {
    FileHandle.standardError.write(Data("error: \(error)\n".utf8))
    exit(2)
}

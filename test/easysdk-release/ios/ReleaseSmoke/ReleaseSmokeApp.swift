import Foundation
import SwiftUI
import WuKongEasySDK

private enum SmokeError: Error {
    case missingEnvironment(String)
}

private final class MessageWaiter {
    private let lock = NSLock()
    private var continuation: CheckedContinuation<Message, Never>?
    private var pending: Message?

    func receive(_ message: Message) {
        lock.lock()
        if let continuation {
            self.continuation = nil
            lock.unlock()
            continuation.resume(returning: message)
            return
        }
        pending = message
        lock.unlock()
    }

    func wait() async -> Message {
        await withCheckedContinuation { continuation in
            lock.lock()
            if let pending {
                self.pending = nil
                lock.unlock()
                continuation.resume(returning: pending)
                return
            }
            self.continuation = continuation
            lock.unlock()
        }
    }
}

@MainActor
private final class ReleaseSmokeState: ObservableObject {
    @Published var status = "RUNNING"
    private var started = false

    func start() {
        guard !started else { return }
        started = true
        Task { await run() }
    }

    private func run() async {
        var sdk: WuKongEasySDK?
        do {
            let environment = ProcessInfo.processInfo.environment
            let aliceUid = try required("ALICE_UID", in: environment)
            let aliceToken = try required("ALICE_TOKEN", in: environment)
            let bobUid = try required("BOB_UID", in: environment)
            let aliceToBobText = try required("ALICE_TO_BOB_TEXT", in: environment)
            let bobToAliceText = try required("BOB_TO_ALICE_TEXT", in: environment)

            let client = try WuKongEasySDK.create(
                serverUrl: "ws://127.0.0.1:5200",
                uid: aliceUid,
                token: aliceToken,
                deviceFlag: .app
            )
            sdk = client
            let waiter = MessageWaiter()
            let listener = client.onMessage { message in
                guard
                    message.fromUid == bobUid,
                    message.payload["content"] as? String == bobToAliceText
                else { return }
                waiter.receive(message)
            }

            try await client.connect()
            let acknowledgment = try await client.send(
                channelId: bobUid,
                channelType: .person,
                payload: MessagePayload(type: 1, content: aliceToBobText)
            )
            let reply = await waiter.wait()

            client.removeListener(listener)
            client.disconnect()

            guard acknowledgment.messageSeq > 0, reply.messageSeq > 0, !client.isConnected else {
                throw NSError(domain: "ReleaseSmoke", code: 1)
            }

            try writeReceipt([
                "status": "PASS",
                "package": "WuKongEasySDK CocoaPods 1.1.1",
                "platform": "iOS Simulator",
                "aliceToBob": true,
                "bobToAlice": true,
                "disconnected": true,
            ])
            status = "PASS"
            print("IOS_RELEASE_SMOKE_PASS package=WuKongEasySDK@1.1.1")
        } catch {
            sdk?.disconnect()
            try? writeReceipt([
                "status": "FAIL",
                "errorType": String(describing: type(of: error)),
            ])
            status = "FAIL"
            print("IOS_RELEASE_SMOKE_FAIL error=\(type(of: error))")
        }
    }

    private func required(_ name: String, in environment: [String: String]) throws -> String {
        guard let value = environment[name], !value.isEmpty else {
            throw SmokeError.missingEnvironment(name)
        }
        return value
    }

    private func writeReceipt(_ receipt: [String: Any]) throws {
        let directory = FileManager.default.urls(for: .documentDirectory, in: .userDomainMask)[0]
        let data = try JSONSerialization.data(withJSONObject: receipt, options: [.sortedKeys])
        try data.write(to: directory.appendingPathComponent("release-smoke.json"), options: .atomic)
    }
}

private struct ContentView: View {
    @StateObject private var state = ReleaseSmokeState()

    var body: some View {
        Text(state.status)
            .task { state.start() }
    }
}

@main
struct ReleaseSmokeApp: App {
    var body: some Scene {
        WindowGroup { ContentView() }
    }
}

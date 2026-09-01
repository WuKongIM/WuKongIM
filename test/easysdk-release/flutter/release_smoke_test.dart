import 'dart:async';

import 'package:flutter_test/flutter_test.dart';
import 'package:integration_test/integration_test.dart';
import 'package:wukong_easy_sdk/wukong_easy_sdk.dart';

void main() {
  IntegrationTestWidgetsFlutterBinding.ensureInitialized();

  testWidgets('released pub package completes bidirectional messaging', (
    tester,
  ) async {
    const aliceUid = String.fromEnvironment('ALICE_UID');
    const aliceToken = String.fromEnvironment('ALICE_TOKEN');
    const bobUid = String.fromEnvironment('BOB_UID');
    const aliceToBobText = String.fromEnvironment('ALICE_TO_BOB_TEXT');
    const bobToAliceText = String.fromEnvironment('BOB_TO_ALICE_TEXT');

    expect(aliceUid, isNotEmpty);
    expect(aliceToken, isNotEmpty);
    expect(bobUid, isNotEmpty);

    final sdk = WuKongEasySDK.getInstance();
    final receivedReply = Completer<Message>();
    late final WuKongEventListener<Message> listener;
    listener = (message) {
      final payload = message.payload;
      if (message.fromUid == bobUid &&
          payload is Map &&
          payload['content'] == bobToAliceText &&
          !receivedReply.isCompleted) {
        receivedReply.complete(message);
      }
    };

    sdk.addEventListener(WuKongEvent.message, listener);
    try {
      await sdk.init(
        const WuKongConfig(
          serverUrl: 'ws://127.0.0.1:5200',
          uid: aliceUid,
          token: aliceToken,
          deviceFlag: WuKongDeviceFlag.app,
        ),
      );
      await sdk.connect().timeout(const Duration(seconds: 30));
      expect(sdk.isConnected, isTrue);

      final acknowledgment = await sdk.send(
        channelId: bobUid,
        channelType: WuKongChannelType.person,
        payload: const {'type': 1, 'content': aliceToBobText},
      );
      expect(acknowledgment.messageSeq, greaterThan(0));

      final reply = await receivedReply.future.timeout(
        const Duration(seconds: 30),
      );
      expect(reply.messageSeq, greaterThan(0));
    } finally {
      sdk.removeEventListener(WuKongEvent.message, listener);
      sdk.disconnect();
    }

    await tester.pump(const Duration(milliseconds: 200));
    expect(sdk.isConnected, isFalse);
    // ignore: avoid_print
    print(
      'FLUTTER_RELEASE_SMOKE_PASS package=wukong_easy_sdk@1.1.0 '
      'alice-to-bob=true bob-to-alice=true disconnected=true',
    );
  });
}

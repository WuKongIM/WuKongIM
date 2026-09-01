import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'package:flutter/material.dart';
import 'package:wukong_easy_sdk/wukong_easy_sdk.dart';

void main() {
  WidgetsFlutterBinding.ensureInitialized();
  runApp(const ReleaseSmokeApp());
  unawaited(_runReleaseSmoke());
}

class ReleaseSmokeApp extends StatelessWidget {
  const ReleaseSmokeApp({super.key});

  @override
  Widget build(BuildContext context) {
    return const MaterialApp(
      home: Scaffold(body: Center(child: Text('WuKongEasySDK release smoke'))),
    );
  }
}

String _requiredConfig(Map<String, dynamic> config, String name) {
  final value = config[name];
  if (value is! String || value.isEmpty) {
    throw StateError('Missing required release smoke setting: $name');
  }
  return value;
}

Future<Map<String, dynamic>> _readConfig(File configFile) async {
  final source = await configFile.readAsString();
  await configFile.delete();
  final decoded = jsonDecode(source);
  if (decoded is! Map<String, dynamic>) {
    throw const FormatException('Release smoke config must be a JSON object');
  }
  return decoded;
}

Future<void> _writeReceipt(File receipt, Map<String, Object> payload) async {
  final temporary = File('${receipt.path}.tmp');
  await temporary.writeAsString(jsonEncode(payload), flush: true);
  await temporary.rename(receipt.path);
}

Future<void> _runReleaseSmoke() async {
  final configFile = File(
    '${Directory.systemTemp.path}/release-smoke-config.json',
  );
  final receipt = File('${Directory.systemTemp.path}/release-smoke.json');
  final sdk = WuKongEasySDK.getInstance();
  WuKongEventListener<Message>? listener;
  var stage = 'config';

  try {
    final config = await _readConfig(configFile);
    final aliceUid = _requiredConfig(config, 'ALICE_UID');
    final aliceToken = _requiredConfig(config, 'ALICE_TOKEN');
    final bobUid = _requiredConfig(config, 'BOB_UID');
    final aliceToBobText = _requiredConfig(config, 'ALICE_TO_BOB_TEXT');
    final bobToAliceText = _requiredConfig(config, 'BOB_TO_ALICE_TEXT');
    final receivedReply = Completer<Message>();

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

    stage = 'init';
    await sdk
        .init(
          WuKongConfig(
            serverUrl: 'ws://127.0.0.1:5200',
            uid: aliceUid,
            token: aliceToken,
            deviceFlag: WuKongDeviceFlag.app,
          ),
        )
        .timeout(const Duration(seconds: 30));
    stage = 'connect';
    await sdk.connect().timeout(const Duration(seconds: 30));
    if (!sdk.isConnected) {
      throw StateError('SDK did not report a connected state');
    }

    stage = 'send';
    final acknowledgment = await sdk
        .send(
          channelId: bobUid,
          channelType: WuKongChannelType.person,
          payload: {'type': 1, 'content': aliceToBobText},
        )
        .timeout(const Duration(seconds: 30));
    if (acknowledgment.messageSeq <= 0) {
      throw StateError(
        'Send acknowledgment did not contain a message sequence',
      );
    }

    stage = 'reply';
    final reply = await receivedReply.future.timeout(
      const Duration(seconds: 30),
    );
    if (reply.messageSeq <= 0) {
      throw StateError('Reply did not contain a message sequence');
    }

    stage = 'disconnect';
    sdk.disconnect();
    await Future<void>.delayed(const Duration(milliseconds: 200));
    if (sdk.isConnected) {
      throw StateError('SDK remained connected after disconnect');
    }

    await _writeReceipt(receipt, {
      'status': 'PASS',
      'package': 'wukong_easy_sdk@1.1.0',
      'alice_to_bob': true,
      'bob_to_alice': true,
      'disconnected': true,
    });
    // ignore: avoid_print
    print(
      'FLUTTER_RELEASE_SMOKE_PASS package=wukong_easy_sdk@1.1.0 '
      'alice-to-bob=true bob-to-alice=true disconnected=true',
    );
  } catch (error) {
    await _writeReceipt(receipt, {
      'status': 'FAIL',
      'stage': stage,
      'error_type': error.runtimeType.toString(),
    });
    // ignore: avoid_print
    print('FLUTTER_RELEASE_SMOKE_FAIL type=${error.runtimeType}');
  } finally {
    if (listener != null) {
      sdk.removeEventListener(WuKongEvent.message, listener);
    }
    if (sdk.isConnected) {
      sdk.disconnect();
    }
  }
}

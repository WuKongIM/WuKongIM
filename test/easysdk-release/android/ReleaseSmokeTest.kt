package com.githubim.easysdk.example

import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import com.githubim.easysdk.WuKongConfig
import com.githubim.easysdk.WuKongEasySDK
import com.githubim.easysdk.enums.WuKongChannelType
import com.githubim.easysdk.enums.WuKongDeviceFlag
import com.githubim.easysdk.enums.WuKongEvent
import com.githubim.easysdk.listener.WuKongEventListener
import com.githubim.easysdk.model.Message
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.withTimeout
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test
import org.junit.runner.RunWith

@RunWith(AndroidJUnit4::class)
class ReleaseSmokeTest {
    @Test
    fun releasedMavenPackageCompletesBidirectionalMessaging() = runBlocking {
        val arguments = InstrumentationRegistry.getArguments()
        val aliceUid = requireArgument(arguments.getString("aliceUid"), "aliceUid")
        val aliceToken = requireArgument(arguments.getString("aliceToken"), "aliceToken")
        val bobUid = requireArgument(arguments.getString("bobUid"), "bobUid")
        val aliceToBobText = requireArgument(
            arguments.getString("aliceToBobText"),
            "aliceToBobText"
        )
        val bobToAliceText = requireArgument(
            arguments.getString("bobToAliceText"),
            "bobToAliceText"
        )

        val sdk = WuKongEasySDK.getInstance()
        val receivedReply = CompletableDeferred<Message>()
        val listener = object : WuKongEventListener<Message> {
            override fun onEvent(data: Message) {
                val payload = data.payload as? Map<*, *> ?: return
                if (
                    data.fromUid == bobUid &&
                    payload["content"] == bobToAliceText &&
                    !receivedReply.isCompleted
                ) {
                    receivedReply.complete(data)
                }
            }
        }

        val config = WuKongConfig.Builder()
            .serverUrl("ws://10.0.2.2:5200")
            .uid(aliceUid)
            .token(aliceToken)
            .deviceFlag(WuKongDeviceFlag.APP)
            .debugLogging(false)
            .build()

        sdk.init(InstrumentationRegistry.getInstrumentation().targetContext, config)
        sdk.addEventListener(WuKongEvent.MESSAGE, listener)

        try {
            sdk.connect()
            assertTrue("Android SDK did not report connected", sdk.isConnected())

            val acknowledgment = sdk.send(
                channelId = bobUid,
                channelType = WuKongChannelType.PERSON,
                payload = mapOf("type" to 1, "content" to aliceToBobText)
            )
            assertTrue("SENDACK did not contain a sequence", acknowledgment.messageSeq > 0)

            val reply = withTimeout(30_000) { receivedReply.await() }
            assertTrue("Reply did not contain a sequence", reply.messageSeq > 0)
        } finally {
            sdk.removeEventListener(WuKongEvent.MESSAGE, listener)
            sdk.disconnect()
        }

        assertFalse("Android SDK remained connected after disconnect", sdk.isConnected())
        println(
            "ANDROID_RELEASE_SMOKE_PASS " +
                "package=com.githubim:easysdk-android:1.0.5 " +
                "alice-to-bob=true bob-to-alice=true disconnected=true"
        )
    }

    private fun requireArgument(value: String?, name: String): String {
        return value?.takeIf { it.isNotBlank() }
            ?: error("Missing instrumentation argument: $name")
    }
}

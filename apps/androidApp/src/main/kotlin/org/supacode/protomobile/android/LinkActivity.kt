package org.supacode.protomobile.android

import android.app.Activity
import android.content.Intent
import android.os.Bundle
import java.util.UUID

/**
 * Receives supaproto:// links and hands them to [MainActivity] with a
 * one-time id. A task relaunched later from its stored launch intent then
 * carries an id that was already handled, so the link is not imported again.
 */
class LinkActivity : Activity() {
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        intent.dataString?.let { link ->
            startActivity(
                Intent(this, MainActivity::class.java)
                    .putExtra(MainActivity.EXTRA_LINK, link)
                    .putExtra(MainActivity.EXTRA_LINK_ID, UUID.randomUUID().toString())
                    .addFlags(Intent.FLAG_ACTIVITY_NEW_TASK or Intent.FLAG_ACTIVITY_SINGLE_TOP),
            )
        }
        finish()
    }
}

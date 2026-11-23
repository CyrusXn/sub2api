package eu.cc.xnkaixin.alerts;

import android.content.BroadcastReceiver;
import android.content.Context;
import android.content.Intent;

public final class UpdateReceiver extends BroadcastReceiver {
    @Override public void onReceive(Context context, Intent intent) {
        AlertUpdater.result(context, intent);
    }
}

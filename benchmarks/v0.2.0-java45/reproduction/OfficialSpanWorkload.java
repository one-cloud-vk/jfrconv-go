import one.profiler.AsyncProfiler;
import one.profiler.Recording;
import one.profiler.Span;
import java.util.concurrent.CountDownLatch;

/** Real recordings produced by the exact public4.5 API, not synthetic JFR bytes. */
public class OfficialSpanWorkload {
    private static volatile long sink;
    private static void busy(long millis) {
        long until=System.nanoTime()+millis*1_000_000L, value=1;
        while(System.nanoTime()<until) for(int i=0;i<10000;i++) value=value*1664525+1013904223+i;
        sink=value;
    }
    private static void workerA() { long t=Span.start(); busy(140); Span.end(t,"worker.a"); }
    private static void workerB() { long t=Span.start(); busy(150); Span.endIfProfiled(t,"worker.b"); }
    public static void main(String[] args) throws Exception {
        AsyncProfiler profiler=AsyncProfiler.getInstance(args[0]);
        Span.end(Span.start(),"before-session");
        profiler.execute("start,event=itimer,interval=1ms,wall=1ms,alloc=64k,lock=1us,file="+args[1]);
        if(Recording.state()!=Recording.RUNNING || Recording.clockFrequency()<=0) throw new AssertionError("recording did not start");
        long outer=Span.start(); busy(60);
        long inner=Span.start(); busy(75); Span.end(inner,"nested.inner");
        Span.end(outer,"request");
        long t=Span.start(); busy(70); Span.end(t,"");
        t=Span.start(); busy(70); Span.end(t,null);
        long explicitStart=Recording.timestamp(); busy(85); long explicitEnd=Recording.timestamp();
        Span.emit(explicitStart,explicitEnd,"emit.explicit");
        Span.emit(explicitStart,explicitEnd,"literal.*");
        long optional=Span.start(); busy(90); Span.endIfProfiled(optional,"optional.busy");
        explicitStart=Recording.timestamp(); busy(90); explicitEnd=Recording.timestamp();
        Span.emitIfProfiled(explicitStart,explicitEnd,"optional.explicit");
        CountDownLatch start=new CountDownLatch(1);
        Thread a=new Thread(()->{try{start.await();}catch(Exception e){throw new RuntimeException(e);}workerA();},"span-worker-a");
        Thread b=new Thread(()->{try{start.await();}catch(Exception e){throw new RuntimeException(e);}workerB();},"span-worker-b");
        a.start();b.start();start.countDown();a.join();b.join();
        // dump,jfr finalizes the current chunk while keeping the same session/file.
        long cross=Span.start(); busy(80); profiler.execute("dump,jfr");
        busy(90); profiler.execute("dump,jfr"); busy(80); Span.end(cross,"cross-chunk");
        long boundary=Span.start(); busy(40); Span.end(boundary,"boundary.left");
        long zero=Recording.timestamp(); Span.emit(zero,zero,"zero-duration");
        boundary=Span.start(); busy(40); Span.end(boundary,"boundary.right");
        profiler.execute("stop");
        Span.end(Span.start(),"after-session");
        if(Recording.state()!=Recording.STOPPED) throw new AssertionError("recording did not stop");
        System.out.println("API=4.5 frequency="+Recording.clockFrequency()+" sink="+sink);
    }
}

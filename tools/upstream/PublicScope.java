import java.util.Arrays;
import one.profiler.test.RunnableTest;
import one.profiler.test.TestDeclaration;

public class PublicScope {
    public static void main(String[] args) throws Exception {
        for (RunnableTest test : TestDeclaration.parse(args).getRunnableTests()) {
            System.out.println(test.testName() + "\t" + Arrays.toString(test.test().os())
                    + "\t" + test.testInfo());
        }
    }
}

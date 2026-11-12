import java.util.Arrays;
import one.profiler.test.*;
public class PublicScope {
 public static void main(String[] args) throws Exception {
  for(RunnableTest t:TestDeclaration.parse(args).getRunnableTests()) {
   System.out.println(t.testName()+"\t"+Arrays.toString(t.test().os())+"\t"+t.testInfo());
  }
 }
}
